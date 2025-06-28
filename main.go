package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp" // Chrome DevTools Protocol: headless browser automation
)

func main() {
	// The remote URL.
	remoteURL := "https://www.gojo.com/en/SDS"
	// Local file name.
	localFileName := "gojo.html"
	// Prepare to download all PDFs
	outputFolder := "PDFs/"
	if !directoryExists(outputFolder) {
		createDirectory(outputFolder, 0o755)
	}
	// Check if the file exists.
	if !fileExists(localFileName) {
		// Send a request to the http url and get the content.
		remoteHTML := scrapePageHTMLWithChrome(remoteURL)
		// Lets save the file content to a local location.
		appendAndWriteToFile(localFileName, remoteHTML)
	}
	// Read the file and than process the data.
	localFileContent := readAFileAsString(localFileName)
	// Process the local file and extract all the .pdf urls.
	extractedLocalPDFURL := extractPDFLinks(localFileContent)
	// Remove duplicates from slice.
	extractedLocalPDFURL = removeDuplicatesFromSlice(extractedLocalPDFURL)
	// Loop over the given data.
	for _, urls := range extractedLocalPDFURL {
		if isUrlValid(urls) {
			downloadPDF(urls, outputFolder)
		}
	}
}

// Append and write to file
func appendAndWriteToFile(path string, content string) {
	filePath, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalln(err)
	}
	_, err = filePath.WriteString(content + "\n")
	if err != nil {
		log.Fatalln(err)
	}
	err = filePath.Close()
	if err != nil {
		log.Fatalln(err)
	}
}

// Uses headless Chrome to get the fully rendered HTML from a webpage
func scrapePageHTMLWithChrome(pageURL string) string {
	log.Println("Scraping:", pageURL)

	// Chrome options for headless mode
	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),               // Run in background
		chromedp.Flag("disable-gpu", true),            // GPU not needed
		chromedp.WindowSize(1920, 1080),               // Simulate large screen
		chromedp.Flag("no-sandbox", true),             // Disable sandbox
		chromedp.Flag("disable-setuid-sandbox", true), // Required for some Linux environments
	)

	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...) // Set allocator
	ctxTimeout, cancelTimeout := context.WithTimeout(allocatorCtx, 5*time.Minute)                // Set timeout
	browserCtx, cancelBrowser := chromedp.NewContext(ctxTimeout)                                 // Create browser context

	defer func() {
		cancelBrowser()
		cancelTimeout()
		cancelAllocator()
	}()

	var pageHTML string // Variable to hold the scraped HTML
	err := chromedp.Run(browserCtx,
		chromedp.Navigate(pageURL),            // Load page
		chromedp.OuterHTML("html", &pageHTML), // Extract full HTML content
	)
	if err != nil {
		log.Println(err)
		return ""
	}

	return pageHTML
}

// extractPDFLinks scans htmlContent line by line and returns all unique .pdf URLs.
func extractPDFLinks(htmlContent string) []string {
	// Regex to match http(s) URLs ending in .pdf (with optional query/fragments)
	pdfRegex := regexp.MustCompile(`https?://[^\s"'<>]+?\.pdf(?:\?[^\s"'<>]*)?`)

	seen := make(map[string]struct{})
	var links []string

	// Process each line separately
	for _, line := range strings.Split(htmlContent, "\n") {
		for _, match := range pdfRegex.FindAllString(line, -1) {
			if _, ok := seen[match]; !ok {
				seen[match] = struct{}{}
				links = append(links, match)
			}
		}
	}

	return links
}

// urlToFilename converts a URL into a filesystem-safe filename
func urlToFilename(rawURL string) string {
	parsed, err := url.Parse(rawURL) // Parse the URL
	// Print the errors if any.
	if err != nil {
		log.Println(err) // Log error
		return ""        // Return empty string on error
	}
	filename := parsed.Host // Start with host name
	// Parse the path and if its not empty replace them with valid characters.
	if parsed.Path != "" {
		filename += "_" + strings.ReplaceAll(parsed.Path, "/", "_") // Append path
	}
	if parsed.RawQuery != "" {
		filename += "_" + strings.ReplaceAll(parsed.RawQuery, "&", "_") // Append query
	}
	invalidChars := []string{`"`, `\`, `/`, `:`, `*`, `?`, `<`, `>`, `|`, `-`} // Define illegal filename characters
	// Loop over the invalid characters and replace them.
	for _, char := range invalidChars {
		filename = strings.ReplaceAll(filename, char, "_") // Replace each with underscore
	}
	if getFileExtension(filename) != ".pdf" {
		filename = filename + ".pdf"
	}
	return strings.ToLower(filename) // Return sanitized filename
}

// Read a file and return the contents
func readAFileAsString(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		log.Println(err)
	}
	return string(content)
}

// downloadPDF downloads a PDF from the given URL and saves it in the specified output directory.
// It uses a WaitGroup to support concurrent execution and returns true if the download succeeded.
func downloadPDF(finalURL, outputDir string) bool {
	// Sanitize the URL to generate a safe file name
	filename := urlToFilename(finalURL)

	// Construct the full file path in the output directory
	filePath := filepath.Join(outputDir, filename)

	// Skip if the file already exists
	if fileExists(filePath) {
		log.Printf("File already exists, skipping: %s", filePath)
		return false
	}

	// Create an HTTP client with a timeout
	client := &http.Client{Timeout: 30 * time.Second}

	// Send GET request
	resp, err := client.Get(finalURL)
	if err != nil {
		log.Printf("Failed to download %s: %v", finalURL, err)
		return false
	}
	defer resp.Body.Close()

	// Check HTTP response status
	if resp.StatusCode != http.StatusOK {
		log.Printf("Download failed for %s: %s", finalURL, resp.Status)
		return false
	}

	// Check Content-Type header
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/pdf") {
		log.Printf("Invalid content type for %s: %s (expected application/pdf)", finalURL, contentType)
		return false
	}

	// Read the response body into memory first
	var buf bytes.Buffer
	written, err := io.Copy(&buf, resp.Body)
	if err != nil {
		log.Printf("Failed to read PDF data from %s: %v", finalURL, err)
		return false
	}
	if written == 0 {
		log.Printf("Downloaded 0 bytes for %s; not creating file", finalURL)
		return false
	}

	// Only now create the file and write to disk
	out, err := os.Create(filePath)
	if err != nil {
		log.Printf("Failed to create file for %s: %v", finalURL, err)
		return false
	}
	defer out.Close()

	if _, err := buf.WriteTo(out); err != nil {
		log.Printf("Failed to write PDF to file for %s: %v", finalURL, err)
		return false
	}

	log.Printf("Successfully downloaded %d bytes: %s → %s", written, finalURL, filePath)
	return true
}

// fileExists checks whether a file exists at the given path
func fileExists(filename string) bool {
	info, err := os.Stat(filename) // Get file info
	if err != nil {
		return false // Return false if file doesn't exist or error occurs
	}
	return !info.IsDir() // Return true if it's a file (not a directory)
}

// Checks if the directory exists
// If it exists, return true.
// If it doesn't, return false.
func directoryExists(path string) bool {
	directory, err := os.Stat(path)
	if err != nil {
		return false
	}
	return directory.IsDir()
}

// The function takes two parameters: path and permission.
// We use os.Mkdir() to create the directory.
// If there is an error, we use log.Println() to log the error and then exit the program.
func createDirectory(path string, permission os.FileMode) {
	err := os.Mkdir(path, permission)
	if err != nil {
		log.Println(err)
	}
}

// Get the file extension of a file
func getFileExtension(path string) string {
	return filepath.Ext(path)
}

// Checks whether a URL string is syntactically valid
func isUrlValid(uri string) bool {
	_, err := url.ParseRequestURI(uri) // Attempt to parse the URL
	return err == nil                  // Return true if no error occurred
}

// Remove all the duplicates from a slice and return the slice.
func removeDuplicatesFromSlice(slice []string) []string {
	check := make(map[string]bool)
	var newReturnSlice []string
	for _, content := range slice {
		if !check[content] {
			check[content] = true
			newReturnSlice = append(newReturnSlice, content)
		}
	}
	return newReturnSlice
}
