package botsky

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
    "os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"

	lexutil "github.com/davhofer/indigo/lex/util"
	"golang.org/x/net/html"
	"golang.org/x/term"
)

func Sleep(seconds int) {
	time.Sleep(time.Duration(seconds) * time.Second)
}

func GetEnvCredentials() (string, string, error) {
	handle := os.Getenv("BOTSKY_HANDLE")
	appkey := os.Getenv("BOTSKY_APPKEY")
	if handle == "" || appkey == "" {
		return "", "", fmt.Errorf("BOTSKY_HANDLE or BOTSKY_APPKEY env variable not set")
	}
	return handle, appkey, nil
}

func GetCLICredentials() (string, string, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Enter account handle: ")
	handle, err := reader.ReadString('\n')
	if err != nil {
		return "", "", err
	}

	fmt.Print("Enter appkey: ")
	byteAppkey, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return "", "", err
	}

	appkey := string(byteAppkey)
	return strings.TrimSpace(handle), strings.TrimSpace(appkey), nil
}

type CBORUnmarshaler interface {
	UnmarshalCBOR(io.Reader) error
}

/*
To call this function, provide it with a non-nil pointer to a variable of the intended result type.
The record will then be decoded "into" the provided variable.
E.g.
```
var postview bsky.FeedDefs_PostView = ...
var post bsky.FeedPost

	if err := botsky.DecodeRecordAsLexicon(postView.Record, &post); err != nil {
	    return
	}

```
*/
func DecodeRecordAsLexicon[ResultPointerType CBORUnmarshaler](recordDecoder *lexutil.LexiconTypeDecoder, resultPointer ResultPointerType) error {
	var buf bytes.Buffer

	if err := recordDecoder.Val.MarshalCBOR(&buf); err != nil {
		return err
	}

	return resultPointer.UnmarshalCBOR(&buf)
}

// This function has been modified from its original version.
// Original source: https://github.com/danrusei/gobot-bsky/blob/main/gobot.go
// License: Apache 2.0
func getImageAsBuffer(imageLocation string) ([]byte, error) {
	if strings.HasPrefix(imageLocation, "http://") || strings.HasPrefix(imageLocation, "https://") {
		// Fetch image from URL
		response, err := http.Get(imageLocation)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()

		// Check response status
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch image: %s", response.Status)
		}

		// Read response body
		imageData, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, err
		}

		return imageData, nil
	} else {
		// Read image from local file
		imageData, err := os.ReadFile(imageLocation)
		if err != nil {
			return nil, err
		}
		return imageData, nil
	}
}

// This function has been modified from its original version.
// Original source: https://github.com/danrusei/gobot-bsky/blob/main/post.go
// License: Apache 2.0
func findSubstring(s, substr string) (int, int, error) {
	index := strings.Index(s, substr)
	if index == -1 {
		return 0, 0, errors.New("substring not found")
	}
	return index, index + len(substr), nil
}

func findRegexMatches(text, pattern string) []struct {
	Value string
	Start int
	End   int
} {
	re := regexp.MustCompile(pattern)
	matches := re.FindAllStringIndex(text, -1)

	var results []struct {
		Value string
		Start int
		End   int
	}
	for _, match := range matches {
		results = append(results, struct {
			Value string
			Start int
			End   int
		}{
			Value: text[match[0]:match[1]],
			Start: match[0],
			End:   match[1],
		})
	}
	return results
}

func fetchOpenGraphTwitterTags(url string) (map[string]string, error) {
	// Initialize the result map
	tags := make(map[string]string)

	// Make HTTP request
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %v", err)
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	// Parse HTML
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %v", err)
	}

	// Traverse the HTML tree
	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "meta" {
			var property, content string

			// Check node attributes
			for _, attr := range n.Attr {
				switch attr.Key {
				case "property":
					if strings.HasPrefix(attr.Val, "og:") {
						property = strings.TrimPrefix(attr.Val, "og:")
					}
				case "name":
					if strings.HasPrefix(attr.Val, "twitter:") {
						property = strings.TrimPrefix(attr.Val, "twitter:")
					}
				case "content":
					content = attr.Val
				}
			}

			// If we found both property and content, add to map
			if property != "" && content != "" {
				tags[property] = content
			}
		}

		// Recursively traverse child nodes
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)
	return tags, nil
}

func stripHashtag(hashtag string) string {
	s := strings.TrimSpace(hashtag)
	s = strings.TrimPrefix(s, "#")
	s = strings.TrimRightFunc(s, unicode.IsPunct)
	return s
}

func WaitUntilCancel() {
    // Create channel for shutdown signals
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    fmt.Println("Waiting until cancelled (Ctrl+C)")

    // Block until we receive a shutdown signal
    <-sigChan
    fmt.Println("\nCancelled")
}
