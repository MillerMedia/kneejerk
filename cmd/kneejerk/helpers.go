package main

import (
	"fmt"
	"github.com/logrusorgru/aurora"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Helper Functions
func debugLog(debug bool, format string, v ...interface{}) {
	if debug {
		fmt.Printf(format, v...)
	}
}

func removeANSI(input string) string {
	return ansiEscape.ReplaceAllString(input, "")
}

func writeOutput(s string) {
	if outputFileWriter == nil {
		return
	}
	if _, err := outputFileWriter.WriteString(s + "\n"); err != nil {
		fmt.Fprintf(os.Stderr, "kneejerk: output write failed: %v\n", err)
		return
	}
	if err := outputFileWriter.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "kneejerk: output flush failed: %v\n", err)
	}
}

func determineSeverity(envVar string) string {
	upper := strings.ToUpper(envVar)
	awsAccessKey := strings.Contains(upper, "AWS") &&
		strings.Contains(upper, "ACCESS") &&
		(strings.Contains(upper, "ID") || strings.Contains(upper, "KEY"))
	if awsAccessKey || strings.Contains(upper, "SECRET") {
		return "high"
	}
	if strings.Contains(upper, "AWS") {
		return "medium"
	}
	if strings.Contains(upper, "API") &&
		(strings.Contains(upper, "URL") || strings.Contains(upper, "HOST") || strings.Contains(upper, "ROOT")) {
		return "low"
	}
	return "info"
}

func colorizeMessage(templateID string, outputType string, severity string, jsURL string, match string) (string, string) {
	templateIDColored := aurora.BrightGreen(templateID).String()
	outputTypeColored := aurora.BrightBlue(outputType).String()
	var severityColored string
	if severity == "high" {
		severityColored = aurora.Red(severity).String()
	} else if severity == "medium" {
		severityColored = aurora.Yellow(severity).String()
	} else if severity == "low" {
		severityColored = aurora.Green(severity).String()
	} else {
		severityColored = aurora.Blue(severity).String()
	}
	coloredMessage := fmt.Sprintf("[%s] [%s] [%s] %s [%s]", templateIDColored, outputTypeColored, severityColored, jsURL, match)
	uncoloredMessage := fmt.Sprintf("[%s] [%s] [%s] %s [%s]", templateID, outputType, severity, jsURL, match)
	return coloredMessage, uncoloredMessage
}

func urlJoin(baseURL string, relURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		// If baseURL parsing fails, return the original baseURL
		return baseURL
	}
	rel, err := url.Parse(relURL)
	if err != nil {
		// If relURL parsing fails, return the original baseURL
		return baseURL
	}
	return u.ResolveReference(rel).String()
}

func isSameBaseDomain(url1, url2 string) bool {
	u1, err1 := url.Parse(url1)
	u2, err2 := url.Parse(url2)

	if err1 != nil || err2 != nil {
		return false
	}

	host1Parts := strings.Split(u1.Host, ".")
	host2Parts := strings.Split(u2.Host, ".")

	if len(host1Parts) < 2 || len(host2Parts) < 2 {
		return false
	}

	domain1 := host1Parts[len(host1Parts)-2] + "." + host1Parts[len(host1Parts)-1]
	domain2 := host2Parts[len(host2Parts)-2] + "." + host2Parts[len(host2Parts)-1]

	return domain1 == domain2
}

func printAPI(debug bool, jsURL string, method string, endpoint string) {
	if len(method) > 12 {
		debugLog(debug, "Debug: Ignoring API path due to method length (possible false positive): [%s, %s]\n", method, endpoint)
		return
	}

	// Parse the jsURL and endpoint
	jsURLParsed, err := url.Parse(jsURL)
	if err != nil {
		debugLog(debug, "Debug: Failed to parse jsURL %s: %v\n", jsURL, err)
		return
	}
	endpointParsed, err := url.Parse(endpoint)

	// If endpoint parsed successfully and it's a full URL, check if the base domain matches
	if err == nil && endpointParsed.IsAbs() {
		if extractBaseDomain(jsURLParsed.Host) != extractBaseDomain(endpointParsed.Host) {
			return
		}
	}

	severity := determineSeverity(endpoint)

	coloredMessage, uncoloredMessage := colorizeMessage("kneejerk", "api", severity, jsURL, fmt.Sprintf(`"%s", "%s"`, method, endpoint))
	fmt.Println(coloredMessage)
	writeOutput(uncoloredMessage)
}

// This function takes a hostname and returns its base domain.
func extractBaseDomain(hostname string) string {
	parts := strings.Split(hostname, ".")
	if len(parts) <= 2 {
		return hostname
	}
	return strings.Join(parts[len(parts)-2:], ".")
}
