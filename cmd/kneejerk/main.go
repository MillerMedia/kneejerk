package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// ASCII Banner
const banner = `
 _  __                _           _    
| |/ /               (_)         | |   
| ' / _ __   ___  ___ _  ___ _ __| | __
|  < | '_ \ / _ \/ _ | |/ _ | '__| |/ /
| . \| | | |  __|  __| |  __| |  |   < 
|_|\_|_| |_|\___|\___| |\___|_|  |_|\_\              
                    |__/                
                               v0.2
`

const maxResponseSize = 50 * 1024 * 1024 // cap JS/sourcemap downloads at 50 MB

// Pattern for .js files (anchored end, allows query string)
var jsFilePattern = regexp.MustCompile(`\.js(?:\?.*)?$`)

// Regex to find API path patterns
var apiPathPattern = regexp.MustCompile(`"(GET|POST|PUT|DELETE|PATCH)",\s*"(/v\d+[^"]*)"`)

// Regex to find //# sourceMappingURL= or //@ sourceMappingURL= comments
var sourceMapURLPattern = regexp.MustCompile(`(?m)^//[#@]\s*sourceMappingURL=([^\s]+)\s*$`)

var foundVars = map[string]struct{}{}

var outputFileWriter *bufio.Writer = nil

var httpClient = &http.Client{Timeout: 30 * time.Second}

// Regex to find environment variables directly assigned
var directEnvVarPattern = regexp.MustCompile(`\b(?:NODE|REACT_APP|AWS)_?[A-Z_]*\b\s*:\s*".*?"`)

func scrapeEnvVars(jsURL string, jsContent string) {
	// First, check for direct assignments
	directMatches := directEnvVarPattern.FindAllString(jsContent, -1)
	for _, match := range directMatches {
		if _, ok := foundVars[match]; !ok {
			foundVars[match] = struct{}{}
			severity := determineSeverity(match)
			coloredMessage, uncoloredMessage := colorizeMessage("kneejerk", "env-var", severity, jsURL, match)
			fmt.Println(coloredMessage)
			writeOutput(uncoloredMessage)
		}
	}
}

// Scrape APIs
func scrapeAPIPaths(jsURL string, jsContent string, debug bool) {
	debugLog(debug, "Debug: Scanning for API paths in %s...\n", jsURL)

	// Check for patterns like "POST", "/v1/accounts:signInWithPhoneNumber",
	matches := apiPathPattern.FindAllStringSubmatch(jsContent, -1)
	for _, match := range matches {
		debugLog(debug, "Debug: Found API path match: %s\n", match)
		if _, ok := foundVars[match[0]]; !ok {
			foundVars[match[0]] = struct{}{}
			printAPI(debug, jsURL, match[1], match[2])
		}
	}

	axiosPathRE := regexp.MustCompile(`axios\.(get|post|put|delete|patch)\(\s*['"]([^'"]+)['"]`)
	fetchPathRE := regexp.MustCompile(`fetch\(\s*['"]([^'"]+)['"],[\s\S]*?{[\s\S]*?method\s*:\s*['"]([^'"]+)['"]`)
	ajaxPathRE := regexp.MustCompile(`\$\.ajax\(\s*{\s*url\s*:\s*['"]([^'"]+)['"],[\s\S]*?type\s*:\s*['"]([^'"]+)['"]`)

	axiosMatches := axiosPathRE.FindAllStringSubmatch(jsContent, -1)

	// Swap method and endpoint in axiosMatches
	for i, match := range axiosMatches {
		if len(match) > 2 {
			axiosMatches[i] = []string{match[0], match[2], match[1]}
		}
	}

	fetchMatches := fetchPathRE.FindAllStringSubmatch(jsContent, -1)
	ajaxMatches := ajaxPathRE.FindAllStringSubmatch(jsContent, -1)

	var allMatches [][]string
	allMatches = append(allMatches, axiosMatches...)
	allMatches = append(allMatches, fetchMatches...)
	allMatches = append(allMatches, ajaxMatches...)

	for _, match := range allMatches {
		if len(match) > 2 {
			method := strings.ToUpper(match[2]) // Convert the method to uppercase
			endpoint := strings.ReplaceAll(match[1], `${}`, "")
			debugLog(debug, "Debug: Found AJAX endpoint: [%s, %s]\n", method, endpoint)
			if _, ok := foundVars[endpoint]; !ok {
				foundVars[endpoint] = struct{}{}
				printAPI(debug, jsURL, method, endpoint)
			}
		}
	}
}

func fetchBody(targetURL string) ([]byte, error) {
	res, err := httpClient.Get(targetURL)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}

	return io.ReadAll(io.LimitReader(res.Body, maxResponseSize))
}

func scrapeJSFiles(u string, debug bool) {
	cleanUrl := removeANSI(u)

	body, err := fetchBody(cleanUrl)
	if err != nil {
		fmt.Printf("Failed to get %s: %v\n", u, err)
		return
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		fmt.Printf("Failed to parse %s: %v\n", u, err)
		return
	}

	processedJs := make(map[string]bool)

	doc.Find("script").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		if src == "" || !strings.Contains(src, "/static/") || !jsFilePattern.MatchString(src) {
			return
		}

		jsURL := urlJoin(u, src)
		if processedJs[jsURL] {
			return
		}
		processedJs[jsURL] = true

		jsContent, err := fetchBody(jsURL)
		if err != nil {
			fmt.Printf("Failed to get %s: %v\n", jsURL, err)
			return
		}

		cleanJsContent := removeANSI(string(jsContent))
		scrapeEnvVars(jsURL, cleanJsContent)
		scrapeAPIPaths(jsURL, cleanJsContent, debug)

		mapMatch := sourceMapURLPattern.FindStringSubmatch(cleanJsContent)
		if mapMatch == nil {
			return
		}

		mapFileUrl := urlJoin(jsURL, mapMatch[1])
		debugLog(debug, "Debug: Fetching source map: %s\n", mapFileUrl)

		mapFileContent, err := fetchBody(mapFileUrl)
		if err != nil {
			fmt.Printf("Failed to get %s: %v\n", mapFileUrl, err)
			return
		}

		// SPAs commonly return index.html for missing .map URLs, so skip
		// any response that isn't a JSON object before attempting to parse.
		if trimmed := bytes.TrimSpace(mapFileContent); len(trimmed) == 0 || trimmed[0] != '{' {
			debugLog(debug, "Debug: Skipping non-JSON source map response: %s\n", mapFileUrl)
			return
		}

		var sourceMap struct {
			SourcesContent []string `json:"sourcesContent"`
		}
		if err := json.Unmarshal(mapFileContent, &sourceMap); err != nil {
			fmt.Printf("Failed to parse source map %s: %v\n", mapFileUrl, err)
			return
		}

		for _, sourceContent := range sourceMap.SourcesContent {
			cleanSourceContent := removeANSI(sourceContent)
			scrapeEnvVars(mapFileUrl, cleanSourceContent)
			scrapeAPIPaths(mapFileUrl, cleanSourceContent, debug)
		}
	})
}

func main() {
	fmt.Print(banner)

	url := flag.String("u", "", "URL of the website to scan")
	list := flag.String("l", "", "Path to a file containing a list of URLs to scan")
	output := flag.String("o", "", "Path to output file")
	debug := flag.Bool("debug", false, "Print debugging statements")
	insecure := flag.Bool("k", false, "Skip TLS certificate verification (insecure)")
	flag.Parse()

	if *insecure {
		httpClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}

	if *output != "" {
		file, err := os.Create(*output)
		if err != nil {
			fmt.Printf("Failed to create %s: %v\n", *output, err)
			return
		}
		defer file.Close()

		outputFileWriter = bufio.NewWriter(file)
	}

	if *url != "" {
		scrapeJSFiles(*url, *debug)
	} else if *list != "" {
		file, err := os.Open(*list)
		if err != nil {
			fmt.Printf("Failed to open %s: %v\n", *list, err)
			return
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			cleanedInput := removeANSI(scanner.Text()) // Remove color codes
			scrapeJSFiles(cleanedInput, *debug)        // Here you don't need to split the input anymore.
		}
		if err := scanner.Err(); err != nil {
			fmt.Printf("Error reading file %s: %v\n", *list, err)
		}
	} else if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			fmt.Println(scanner.Text())                // print the input before processing
			cleanedInput := removeANSI(scanner.Text()) // Remove color codes
			writeOutput(cleanedInput)
			urlParts := strings.Split(cleanedInput, " ")
			if len(urlParts) > 3 {
				scrapeJSFiles(urlParts[3], *debug)
			} else {
				fmt.Println("Invalid input:", cleanedInput)
			}
		}
		if err := scanner.Err(); err != nil {
			fmt.Printf("Error reading from stdin: %v\n", err)
		}
	}
}
