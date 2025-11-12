package main

import (
	"bufio"
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

// Pattern for .js files
var jsFilePattern = regexp.MustCompile(`.*\.js`)

// Regex to find API path patterns
var apiPathPattern = regexp.MustCompile(`"(GET|POST|PUT|DELETE|PATCH)",\s*"(/v\d+[^"]*)"`)

var foundVars = map[string]struct{}{}

var outputFileWriter *bufio.Writer = nil

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
			if outputFileWriter != nil {
				_, _ = outputFileWriter.WriteString(uncoloredMessage + "\n")
				_ = outputFileWriter.Flush()
			}
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
		if len(match) > 1 {
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

func scrapeJSFiles(u string, debug bool) {
	// Remove ANSI escape sequences from the URL
	cleanUrl := removeANSI(u)

	res, err := http.Get(cleanUrl)
	if err != nil {
		fmt.Printf("Failed to get %s: %v\n", u, err)
		return
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		fmt.Printf("Failed to parse %s: %v\n", u, err)
		return
	}

	processedJs := make(map[string]bool)

	doc.Find("script").Each(func(i int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		if src != "" && strings.Contains(src, "/static/") && jsFilePattern.MatchString(src) {
			jsURL := urlJoin(u, src)

			// Skip if this JS file has been processed
			if processedJs[jsURL] {
				return
			}
			processedJs[jsURL] = true

			jsRes, err := http.Get(jsURL)
			if err != nil {
				fmt.Printf("Failed to get %s: %v\n", jsURL, err)
				return
			}
			defer jsRes.Body.Close()

			jsContent, err := io.ReadAll(jsRes.Body)
			if err != nil {
				fmt.Printf("Failed to read %s: %v\n", jsURL, err)
				return
			}

			// Remove ANSI escape sequences
			cleanJsContent := removeANSI(string(jsContent))

			// Call the specific scraping functions
			scrapeEnvVars(jsURL, cleanJsContent)
			scrapeAPIPaths(jsURL, cleanJsContent, debug)

			// Check for sourceMappingURL
			if strings.HasSuffix(cleanJsContent, ".map") {
				lines := strings.Split(cleanJsContent, "\n")
				lastLine := lines[len(lines)-1]
				if strings.HasPrefix(lastLine, "//# sourceMappingURL=") {
					mapFileName := strings.TrimPrefix(lastLine, "//# sourceMappingURL=")
					mapFileUrl := urlJoin(jsURL, mapFileName)
					debugLog(debug, "Debug: Fetching source map: %s\n", mapFileUrl)
					mapFileRes, err := http.Get(mapFileUrl)
					if err != nil {
						fmt.Printf("Failed to get %s: %v\n", mapFileUrl, err)
						return
					}
					defer mapFileRes.Body.Close()

					mapFileContent, err := io.ReadAll(mapFileRes.Body)
					if err != nil {
						fmt.Printf("Failed to read %s: %v\n", mapFileUrl, err)
						return
					}

					var sourceMap struct {
						SourcesContent []string `json:"sourcesContent"`
					}

					err = json.Unmarshal(mapFileContent, &sourceMap)
					if err != nil {
						fmt.Printf("Failed to parse source map %s: %v\n", mapFileUrl, err)
						return
					}

					for _, sourceContent := range sourceMap.SourcesContent {
						// Remove ANSI escape sequences
						cleanSourceContent := removeANSI(sourceContent)

						// Call the specific scraping functions
						scrapeEnvVars(mapFileUrl, cleanSourceContent)
						scrapeAPIPaths(mapFileUrl, cleanSourceContent, debug)
					}
				}
			}
		}
	})
}

func main() {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	fmt.Print(banner)

	url := flag.String("u", "", "URL of the website to scan")
	list := flag.String("l", "", "Path to a file containing a list of URLs to scan")
	output := flag.String("o", "", "Path to output file")
	debug := flag.Bool("debug", false, "Print debugging statements")
	flag.Parse()

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
			if outputFileWriter != nil {
				_, _ = outputFileWriter.WriteString(cleanedInput + "\n")
				_ = outputFileWriter.Flush()
			}
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
