package yurllib

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
)

type detail struct {
	AppID string   `json:"appID"`
	Paths []string `json:"paths"`
}
type appLinks struct {
	Apps    []string `json:"apps"`
	Details []detail `json:"details"`
}

type aasaFile struct {
	Applinks *appLinks `json:"applinks"`
}

// CheckDomain : Main function used by CLI and WebApp
func CheckDomain(inputURL string, bundleIdentifier string, teamIdentifier string, allowUnencrypted bool) []string {

	var output []string

	//Clean up domains, removing scheme and path
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		output = append(output, fmt.Sprintf("The URL failed to parse with error %s \n", err))
	}

	scheme := parsedURL.Scheme
	var cleanedDomain string

	if scheme != "https" {
		output = append(output, fmt.Sprintf("WARNING: The URL must use HTTPS, trying HTTPS instead. \n\n"))

		parsedURL.Scheme = "https"
		parsedURL, err := url.Parse(parsedURL.String())
		if err != nil {
			output = append(output, fmt.Sprintf("The URL failed to parse with error %s \n", err))
		}

		cleanedDomain = parsedURL.Host

		// debug
		// output = append(output, fmt.Sprintf("isAbsolute: %t \n", parsedURL.IsAbs()))
		// output = append(output, fmt.Sprintf("parsedURL: %s \n", parsedURL))
		// output = append(output, fmt.Sprintf("String: %s \n", parsedURL.String()))
		// output = append(output, fmt.Sprintf("Host: %s \n", parsedURL.Host))
		// output = append(output, fmt.Sprintf("Scheme: %s \n", parsedURL.Scheme))
		// output = append(output, fmt.Sprintf("opaque: %s \n\n", parsedURL.Opaque))
	} else {
		cleanedDomain = parsedURL.Host
	}

	// call loadAASAContents and handle response
	result, message, errors := loadAASAContents(cleanedDomain)
	if len(errors) > 0 {
		for _, e := range errors {
			output = append(output, fmt.Sprintf("  %s\n", e))
		}
		return output
	}
	defer result.Body.Close()

	output = append(output, message...)

	contentType := result.Header["Content-Type"]

	isEncryptedMimeType := contentType[0] == "application/pkcs7-mime"
	isJSONMimeType := contentType[0] == "application/json" || contentType[0] == "text/json"
	isJSONTypeOK := allowUnencrypted && isJSONMimeType // Only ok if both the "allow" flag is true, and... it's a valid type.

	if !isEncryptedMimeType && !isJSONTypeOK {
		output = append(output, fmt.Sprintf("Invalid content-type: %s \n", contentType[0]))
		//return nil or error
	}

	if allowUnencrypted {
		// Try to decode the JSON right away (this assumes the file is not encrypted)
		// If it's not encrypted, we'll just return it
		output = append(output, evaluateAASA(result, bundleIdentifier, teamIdentifier, false)...)

	} else {
		// Decrypt and evaluate file
	}

	return output
}

func loadAASAContents(domain string) (*http.Response, []string, []error) {

	var output []string
	var formatErrors []error
	var respStatus int

	wellKnownPath := "https://" + domain + "/.well-known/apple-app-site-association"
	aasaPath := "https://" + domain + "/apple-app-site-association"

	resp, err := makeRequest(wellKnownPath)
	if err == nil {
		respStatus = resp.StatusCode

		if respStatus >= 200 && respStatus < 300 {
			output = append(output, fmt.Sprintf("Found file at:\n  %s\n\n", wellKnownPath))
			output = append(output, fmt.Sprintln("No Redirect: \t\t\t  Pass"))
			return resp, output, nil
		}
	} else {
		formatErrors = append(formatErrors, fmt.Errorf("Error: %w", err))
	}

	resp, err = makeRequest(aasaPath)
	if err == nil {
		respStatus = resp.StatusCode

		if respStatus >= 200 && respStatus < 300 {
			output = append(output, fmt.Sprintf("Found file at:\n  %s\n\n", aasaPath))
			output = append(output, fmt.Sprintln("No Redirect: \t\t\t Pass"))
			return resp, output, nil
		}
	} else {
		formatErrors = append(formatErrors, fmt.Errorf("Error: %w", err))
	}

	formatErrors = append(formatErrors, errors.New("could not find file in either known locations"))

	return nil, output, formatErrors
}

func makeRequest(fileURL string) (*http.Response, error) {

	resp, err := http.Get(fileURL)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func evaluateAASA(result *http.Response, bundleIdentifier string, teamIdentifier string, encrypted bool) []string {

	var output []string

	jsonText, err := ioutil.ReadAll(result.Body)
	if err != nil {
		output = append(output, fmt.Sprintf("ioutil.ReadAll failed to parse with error: \n%w", err)) //define this better
		return output
	}

	var reqResp aasaFile

	err = json.Unmarshal(jsonText, &reqResp)
	if err != nil {
		prettyJSON, err := json.MarshalIndent(jsonText, "", "    ")
		if err != nil {
			output = append(output, fmt.Sprintf("Failed to print contents with error: %w", err))
			return output
		}
		output = append(output, fmt.Sprintln("JSON Validation: Fail"))

		output = append(output, fmt.Sprintf("%s\n", string(prettyJSON)))

		return output
	}

	output = append(output, fmt.Sprintln("JSON Validation: \t\t  Pass"))

	validJSON, formatErrors := verifyJSONformat(reqResp)

	if validJSON {
		output = append(output, fmt.Sprintln("JSON Schema: \t\t\t  Pass"))

		if bundleIdentifier != "" {
			if verifyBundleIdentifierIsPresent(reqResp, bundleIdentifier, teamIdentifier) {
				output = append(output, fmt.Sprintln("Team/Bundle availability: Pass"))
			} else {
				output = append(output, fmt.Sprintln("Team/Bundle availability: Fail"))
			}
		}

		prettyJSON, err := json.MarshalIndent(reqResp, "", "    ")
		if err != nil {
			output = append(output, fmt.Sprintf("Failed to print contents with error: %w", err))
			return output
		}
		output = append(output, fmt.Sprintf("\n%s\n", string(prettyJSON)))

	} else {
		output = append(output, fmt.Sprintln("JSON Schema: Fail"))
		for _, formatError := range formatErrors {
			output = append(output, fmt.Sprintf("  %s\n", formatError))
		}
		return output
	}

	return output

}

func verifyJSONformat(content aasaFile) (bool, []error) {

	appLinks := content.Applinks

	var formatErrors []error

	if appLinks == nil {
		formatErrors = append(formatErrors, errors.New("missing applinks region"))
	}

	apps := appLinks.Apps
	if apps == nil {
		formatErrors = append(formatErrors, errors.New("missing applinks/apps region"))
	} else if len(apps) > 0 {
		formatErrors = append(formatErrors, errors.New("the apps key must have its value be an empty array"))
	}

	details := appLinks.Details
	if details == nil {
		formatErrors = append(formatErrors, errors.New("missing applinks/details region"))
	}

	if len(formatErrors) > 0 {
		return false, formatErrors
	}

	return true, formatErrors

}

func verifyBundleIdentifierIsPresent(content aasaFile, bundleIdentifier string, teamIdentifier string) bool {

	details := content.Applinks.Details
	matcher := bundleIdentifier + "." + teamIdentifier

	for i := 0; i < len(details); i++ {
		var detail = details[i]
		if detail.AppID == matcher && len(detail.Paths) > 0 {
			return true
		}
	}

	return false
}
