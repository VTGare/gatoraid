package youtube

import "net/url"

// YouTube shows long links shortened and points them at a redirect whose q
// parameter has the real URL. Links to YouTube itself return "", since
// their text is already the link.
func LinkTarget(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}

	if q := u.Query().Get("q"); q != "" {
		return q
	}

	if u.Host != "" && u.Host != "www.youtube.com" {
		return endpoint
	}

	return ""
}
