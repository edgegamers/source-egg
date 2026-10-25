package addons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Package struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	Digest  string `json:"digest,omitempty"`
	Stale   bool   `json:"stale,omitempty"`
}
type Resolver struct {
	HTTP *http.Client
	API  string
}
type release struct {
	Tag    string `json:"tag_name"`
	Pre    bool   `json:"prerelease"`
	Draft  bool   `json:"draft"`
	Assets []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

func OfficialURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	switch u.Hostname() {
	case "github.com", "api.github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}
func HTTPClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !OfficialURL(r.URL.String()) {
			return errors.New("unapproved release redirect")
		}
		return nil
	}}
}
func (r Resolver) Resolve(ctx context.Context, name, pin string) (Package, error) {
	repo := ""
	prefix := ""
	pre := false
	switch name {
	case "metamod":
		repo = "metamod-source"
		prefix = "mmsource-"
	case "sourcemod":
		repo = "sourcemod"
		prefix = "sourcemod-"
		pre = true
	default:
		return Package{}, errors.New("unsupported framework")
	}
	api := r.API
	if api == "" {
		api = "https://api.github.com"
	}
	client := r.HTTP
	if client == nil {
		client = HTTPClient()
	}
	path := api + "/repos/alliedmodders/" + repo + "/releases?per_page=100"
	if pin != "" {
		if strings.ContainsAny(pin, "/\\?#\r\n") {
			return Package{}, errors.New("invalid exact release pin")
		}
		path = api + "/repos/alliedmodders/" + repo + "/releases/tags/" + url.PathEscape(pin)
	}
	req, e := http.NewRequestWithContext(ctx, "GET", path, nil)
	if e != nil {
		return Package{}, e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "source-egg")
	resp, e := client.Do(req)
	if e != nil {
		return Package{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Package{}, fmt.Errorf("release lookup HTTP %d", resp.StatusCode)
	}
	var list []release
	if pin != "" {
		var x release
		if e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&x); e != nil {
			return Package{}, e
		}
		list = []release{x}
	} else {
		if e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); e != nil {
			return Package{}, e
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return newer(list[i].Tag, list[j].Tag) })
	for _, rel := range list {
		if rel.Draft || (pin == "" && rel.Pre != pre) || (pin != "" && rel.Tag != pin) {
			continue
		}
		for _, a := range rel.Assets {
			if strings.HasPrefix(a.Name, prefix) && strings.HasSuffix(a.Name, "-linux.tar.gz") && OfficialURL(a.URL) {
				return Package{name, rel.Tag, a.URL, a.Digest, false}, nil
			}
		}
	}
	return Package{}, errors.New("no matching Linux release; exact pins never fall back to latest")
}
func newer(a, b string) bool {
	aa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(aa) && i < len(bb); i++ {
		x, _ := strconv.Atoi(aa[i])
		y, _ := strconv.Atoi(bb[i])
		if x != y {
			return x > y
		}
	}
	return len(aa) > len(bb)
}
