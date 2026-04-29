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

// Browser-shaped UA so basic WAFs don't reject the default Go-http-client/1.1.
const defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

var userAgent = defaultUserAgent

// Optional Cookie header value, e.g. a cf_clearance grabbed from a real browser
// after solving a Cloudflare challenge.
var cookieHeader string

// Pattern for JS bundle files. Anchored at the end and allowing a query string,
// matches both classic .js and ES-module .mjs extensions.
var jsFilePattern = regexp.MustCompile(`\.m?js(?:\?.*)?$`)

// Path fragments that mark a JS bundle as belonging to the host application
// rather than a third-party widget. Covers React/CRA, Next.js, Vite/Remix,
// SvelteKit, and Astro out of the box.
var bundlePathPrefixes = []string{
	"/static/",
	"/_next/static/",
	"/assets/",
	"/_app/immutable/",
	"/_astro/",
}

// Regex to find API path patterns of the form "METHOD", "/path"
var apiPathPattern = regexp.MustCompile(`"(GET|POST|PUT|DELETE|PATCH)",\s*"(/[^"]+)"`)

// Regex to find //# sourceMappingURL= or //@ sourceMappingURL= comments
var sourceMapURLPattern = regexp.MustCompile(`(?m)^//[#@]\s*sourceMappingURL=([^\s]+)\s*$`)

var foundVars = map[string]struct{}{}

var outputFileWriter *bufio.Writer = nil

var httpClient = &http.Client{Timeout: 30 * time.Second}

// Regex to find environment variables directly assigned. Covers the standard
// build-time prefixes for the major JS frameworks: Node, CRA (REACT_APP_),
// Next.js (NEXT_PUBLIC_), Vite (VITE_), Expo (EXPO_PUBLIC_), plus AWS-prefixed
// vars that frequently leak into bundles.
var directEnvVarPattern = regexp.MustCompile(`\b(?:NODE|REACT_APP|NEXT_PUBLIC|EXPO_PUBLIC|VITE|AWS)_?[A-Z_]*\b\s*:\s*".*?"`)

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
		if !looksLikeURL(match[2]) {
			continue
		}
		if _, ok := foundVars[match[0]]; !ok {
			foundVars[match[0]] = struct{}{}
			printAPI(debug, jsURL, match[1], match[2])
		}
	}

	// All URL-quoting variants below accept ', ", and ` (template literal) delimiters.
	axiosPathRE := regexp.MustCompile("axios\\.(get|post|put|delete|patch)\\(\\s*[\"'`]([^\"'`]+)[\"'`]")
	fetchPathRE := regexp.MustCompile("fetch\\(\\s*[\"'`]([^\"'`]+)[\"'`],[\\s\\S]*?\\{[\\s\\S]*?method\\s*:\\s*[\"'`]([^\"'`]+)[\"'`]")
	fetchBareRE := regexp.MustCompile("fetch\\(\\s*[\"'`]([^\"'`]+)[\"'`]\\s*\\)")
	ajaxPathRE := regexp.MustCompile("\\$\\.ajax\\(\\s*\\{\\s*url\\s*:\\s*[\"'`]([^\"'`]+)[\"'`],[\\s\\S]*?type\\s*:\\s*[\"'`]([^\"'`]+)[\"'`]")
	xhrPathRE := regexp.MustCompile("\\.open\\s*\\(\\s*[\"'`](GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)[\"'`]\\s*,\\s*[\"'`]([^\"'`]+)[\"'`]")

	axiosMatches := axiosPathRE.FindAllStringSubmatch(jsContent, -1)
	// Reorder axios captures to [_, URL, METHOD]
	for i, match := range axiosMatches {
		if len(match) > 2 {
			axiosMatches[i] = []string{match[0], match[2], match[1]}
		}
	}

	fetchMatches := fetchPathRE.FindAllStringSubmatch(jsContent, -1)
	ajaxMatches := ajaxPathRE.FindAllStringSubmatch(jsContent, -1)

	xhrMatches := xhrPathRE.FindAllStringSubmatch(jsContent, -1)
	// Reorder xhr captures to [_, URL, METHOD]
	for i, match := range xhrMatches {
		if len(match) > 2 {
			xhrMatches[i] = []string{match[0], match[2], match[1]}
		}
	}

	// Bare fetch defaults to GET; pad to [_, URL, METHOD]
	fetchBareMatches := fetchBareRE.FindAllStringSubmatch(jsContent, -1)
	for i, match := range fetchBareMatches {
		if len(match) > 1 {
			fetchBareMatches[i] = []string{match[0], match[1], "GET"}
		}
	}

	var allMatches [][]string
	allMatches = append(allMatches, axiosMatches...)
	allMatches = append(allMatches, fetchMatches...)
	allMatches = append(allMatches, ajaxMatches...)
	allMatches = append(allMatches, xhrMatches...)
	allMatches = append(allMatches, fetchBareMatches...)

	for _, match := range allMatches {
		if len(match) > 2 {
			method := strings.ToUpper(match[2]) // Convert the method to uppercase
			endpoint := strings.ReplaceAll(match[1], `${}`, "")
			debugLog(debug, "Debug: Found AJAX endpoint: [%s, %s]\n", method, endpoint)
			if !looksLikeURL(endpoint) {
				continue
			}
			if _, ok := foundVars[endpoint]; !ok {
				foundVars[endpoint] = struct{}{}
				printAPI(debug, jsURL, method, endpoint)
			}
		}
	}
}

func fetchBody(targetURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}

	res, err := httpClient.Do(req)
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
		if src == "" || !jsFilePattern.MatchString(src) || !hasBundlePathPrefix(src) {
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
	ua := flag.String("ua", defaultUserAgent, "User-Agent header to send")
	cookie := flag.String("cookie", "", "Cookie header value to send (e.g. cf_clearance=... from a browser session)")
	flag.Parse()

	if *insecure {
		httpClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	userAgent = *ua
	cookieHeader = *cookie

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
