package main // Declare main package

import ( // Import required packages
	"bytes"         // For in-memory byte buffer
	"context"       // For managing context (timeouts, cancellations)
	"io"            // For input/output utilities
	"log"           // For logging errors/info
	"net/http"      // For HTTP client
	"net/url"       // For URL parsing and manipulation
	"os"            // For file and directory handling
	"path/filepath" // For OS-independent path operations
	"regexp"        // For regular expressions
	"strings"       // For string manipulation
	"time"          // For timing and delays

	"github.com/chromedp/chromedp" // For headless browser automation using Chrome
)

func main() {
	remoteURL := "https://www.gojo.com/en/SDS" // Remote web page URL to scrape
	localFileName := "gojo.html"               // Local file name to save HTML
	outputFolder := "PDFs/"                    // Directory to store downloaded PDFs

	if !directoryExists(outputFolder) { // Check if output folder exists
		createDirectory(outputFolder, 0o755) // If not, create it with permission
	}

	if !fileExists(localFileName) { // If local HTML file doesn't exist
		remoteHTML := scrapePageHTMLWithChrome(remoteURL) // Scrape page using headless Chrome
		appendAndWriteToFile(localFileName, remoteHTML)   // Save scraped HTML to file
	}

	localFileContent := readAFileAsString(localFileName)                   // Read saved HTML content
	extractedLocalPDFURL := extractPDFLinks(localFileContent)              // Extract all PDF links
	extractedLocalPDFURL = removeDuplicatesFromSlice(extractedLocalPDFURL) // Remove duplicates

	for _, urls := range extractedLocalPDFURL { // Loop through each PDF URL
		if isUrlValid(urls) { // Check if URL is valid
			downloadPDF(urls, outputFolder) // Download the PDF
		}
	}
}

// Writes the given content to file, appending if file already exists
func appendAndWriteToFile(path string, content string) {
	filePath, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) // Open or create file
	if err != nil {
		log.Println(err) // Exit if error
	}
	_, err = filePath.WriteString(content + "\n") // Write content to file
	if err != nil {
		log.Println(err)
	}
	err = filePath.Close() // Close the file
	if err != nil {
		log.Println(err)
	}
}

// Uses headless Chrome via chromedp to get fully rendered HTML from a page
func scrapePageHTMLWithChrome(pageURL string) string {
	log.Println("Scraping:", pageURL) // Log page being scraped

	options := append(chromedp.DefaultExecAllocatorOptions[:], // Chrome options
		chromedp.Flag("headless", false),              // Run visible (set to true for headless)
		chromedp.Flag("disable-gpu", true),            // Disable GPU
		chromedp.WindowSize(1920, 1080),               // Set window size
		chromedp.Flag("no-sandbox", true),             // Disable sandbox
		chromedp.Flag("disable-setuid-sandbox", true), // Fix for Linux environments
	)

	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...) // Allocator context
	ctxTimeout, cancelTimeout := context.WithTimeout(allocatorCtx, 5*time.Minute)                // Set timeout
	browserCtx, cancelBrowser := chromedp.NewContext(ctxTimeout)                                 // Create Chrome context

	defer func() { // Ensure all contexts are cancelled
		cancelBrowser()
		cancelTimeout()
		cancelAllocator()
	}()

	var pageHTML string // Placeholder for output
	err := chromedp.Run(browserCtx,
		chromedp.Navigate(pageURL),            // Navigate to the URL
		chromedp.OuterHTML("html", &pageHTML), // Extract full HTML
	)
	if err != nil {
		log.Println(err) // Log error
		return ""        // Return empty string on failure
	}

	return pageHTML // Return scraped HTML
}

// Extracts all PDF URLs from the given HTML content
func extractPDFLinks(htmlContent string) []string {
	pdfRegex := regexp.MustCompile(`https?://[^\s"'<>]+?\.pdf(?:\?[^\s"'<>]*)?`) // Regex for PDF URLs

	seen := make(map[string]struct{}) // To keep track of seen URLs
	var links []string                // Slice to store unique URLs

	for _, line := range strings.Split(htmlContent, "\n") { // Process line by line
		for _, match := range pdfRegex.FindAllString(line, -1) { // Find all matches
			if _, ok := seen[match]; !ok { // If not already seen
				seen[match] = struct{}{}     // Mark as seen
				links = append(links, match) // Add to list
			}
		}
	}

	return links // Return list of PDF URLs
}

// Converts a URL to a filesystem-safe file name
func urlToFilename(rawURL string) string {
	parsed, err := url.Parse(rawURL) // Parse the URL
	if err != nil {
		log.Println(err)
		return ""
	}
	filename := parsed.Host // Start with host
	if parsed.Path != "" {
		filename += "_" + strings.ReplaceAll(parsed.Path, "/", "_") // Add path
	}
	if parsed.RawQuery != "" {
		filename += "_" + strings.ReplaceAll(parsed.RawQuery, "&", "_") // Add query
	}
	invalidChars := []string{`"`, `\`, `/`, `:`, `*`, `?`, `<`, `>`, `|`, `-`} // Invalid filename characters
	for _, char := range invalidChars {
		filename = strings.ReplaceAll(filename, char, "_") // Replace with underscore
	}
	if getFileExtension(filename) != ".pdf" { // Ensure .pdf extension
		filename += ".pdf"
	}
	return strings.ToLower(filename) // Return lowercased name
}

// Reads an entire file as string
func readAFileAsString(path string) string {
	content, err := os.ReadFile(path) // Read file
	if err != nil {
		log.Println(err)
	}
	return string(content) // Return content as string
}

// Downloads a PDF and saves it to the output directory
func downloadPDF(finalURL, outputDir string) bool {
	filename := urlToFilename(finalURL)            // Create safe file name
	filePath := filepath.Join(outputDir, filename) // Full path

	if fileExists(filePath) { // Skip if file already exists
		log.Printf("File already exists, skipping: %s", filePath)
		return false
	}

	client := &http.Client{Timeout: 30 * time.Second} // Create HTTP client

	resp, err := client.Get(finalURL) // Make GET request
	if err != nil {
		log.Printf("Failed to download %s: %v", finalURL, err)
		return false
	}
	defer resp.Body.Close() // Ensure response body is closed

	if resp.StatusCode != http.StatusOK { // Check for 200 OK
		log.Printf("Download failed for %s: %s", finalURL, resp.Status)
		return false
	}

	contentType := resp.Header.Get("Content-Type") // Check Content-Type
	if !strings.Contains(contentType, "application/pdf") {
		log.Printf("Invalid content type for %s: %s (expected application/pdf)", finalURL, contentType)
		return false
	}

	var buf bytes.Buffer                     // Temporary buffer
	written, err := io.Copy(&buf, resp.Body) // Read response body
	if err != nil {
		log.Printf("Failed to read PDF data from %s: %v", finalURL, err)
		return false
	}
	if written == 0 {
		log.Printf("Downloaded 0 bytes for %s; not creating file", finalURL)
		return false
	}

	out, err := os.Create(filePath) // Create file on disk
	if err != nil {
		log.Printf("Failed to create file for %s: %v", finalURL, err)
		return false
	}
	defer out.Close()

	if _, err := buf.WriteTo(out); err != nil { // Write buffer to file
		log.Printf("Failed to write PDF to file for %s: %v", finalURL, err)
		return false
	}

	log.Printf("Successfully downloaded %d bytes: %s → %s", written, finalURL, filePath)
	return true
}

// Checks if a file exists
func fileExists(filename string) bool {
	info, err := os.Stat(filename) // Get file info
	if err != nil {
		return false // Doesn't exist
	}
	return !info.IsDir() // Return true if it's a file
}

// Checks if a directory exists
func directoryExists(path string) bool {
	directory, err := os.Stat(path) // Get file info
	if err != nil {
		return false // Doesn't exist
	}
	return directory.IsDir() // Return true if it's a directory
}

// Creates a directory with given permission
func createDirectory(path string, permission os.FileMode) {
	err := os.Mkdir(path, permission) // Try to create directory
	if err != nil {
		log.Println(err)
	}
}

// Gets file extension
func getFileExtension(path string) string {
	return filepath.Ext(path) // Return file extension
}

// Checks if a URL is valid
func isUrlValid(uri string) bool {
	_, err := url.ParseRequestURI(uri) // Try to parse URI
	return err == nil                  // True if parsing succeeded
}

// Removes duplicate entries from a string slice
func removeDuplicatesFromSlice(slice []string) []string {
	check := make(map[string]bool) // Map to track seen strings
	var newReturnSlice []string    // Slice to store unique values
	for _, content := range slice {
		if !check[content] {
			check[content] = true
			newReturnSlice = append(newReturnSlice, content)
		}
	}
	return newReturnSlice
}
