package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxPluginDownload = int64(256 << 20)
const maxReleaseMetadata = int64(8 << 20)

// HTTPSource acquires release assets without executing package code. Credentials
// are per-server GitLab personal access tokens, keyed by Source.Credential.
type HTTPSource struct {
	Client      *http.Client
	Credentials map[string]string
}

type acquisitionRelease struct {
	version, id       string
	published         time.Time
	prerelease, draft bool
	assets            []Asset
}

type acquisitionGitHubRelease struct {
	ID         int64     `json:"id"`
	Tag        string    `json:"tag_name"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
	Published  time.Time `json:"published_at"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

type acquisitionGitLabRelease struct {
	Tag      string    `json:"tag_name"`
	Upcoming bool      `json:"upcoming_release"`
	Released time.Time `json:"released_at"`
	Assets   struct {
		Links []struct {
			Name      string `json:"name"`
			URL       string `json:"url"`
			DirectURL string `json:"direct_asset_url"`
		} `json:"links"`
	} `json:"assets"`
}

var acquisitionPreTag = regexp.MustCompile(`^[vV]?[0-9]+\.[0-9]+(?:\.[0-9]+)?-[0-9A-Za-z]`)

func (s HTTPSource) Resolve(ctx context.Context, p Plugin) (Release, error) {
	if err := ValidatePlugin(p); err != nil {
		return Release{}, err
	}
	base, token, err := s.connection(p)
	if err != nil {
		return Release{}, err
	}
	endpoint := strings.TrimRight(base.String(), "/")
	if p.Source.Provider == "github" {
		endpoint += "/repos/" + p.Source.Repository + "/releases"
		if p.Pin != "" {
			endpoint += "/tags/" + url.PathEscape(p.Pin)
		}
	} else {
		endpoint += "/api/v4/projects/" + url.PathEscape(p.Source.Repository) + "/releases"
		if p.Pin != "" {
			endpoint += "/" + url.PathEscape(p.Pin)
		}
	}
	// Searching is bounded. Exact tags use the provider's direct endpoint and
	// never silently fall back to a different version.
	for page := 1; page <= 10; page++ {
		requestURL := endpoint
		if p.Pin == "" {
			requestURL += "?per_page=100&page=" + strconv.Itoa(page)
		}
		response, err := s.request(ctx, requestURL, base, token, "application/json")
		if err != nil {
			return Release{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, maxReleaseMetadata+1))
		response.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return Release{}, ctx.Err()
			}
			return Release{}, errors.New("could not read release metadata")
		}
		if int64(len(data)) > maxReleaseMetadata {
			return Release{}, errors.New("release metadata exceeds 8 MiB")
		}
		releases, err := acquisitionDecode(data, p.Source.Provider, p.Pin != "")
		if err != nil {
			return Release{}, err
		}
		sort.SliceStable(releases, func(i, j int) bool { return releases[i].published.After(releases[j].published) })
		for _, release := range releases {
			if release.draft || (p.Pin != "" && release.version != p.Pin) || (p.Pin == "" && !p.Prerelease && release.prerelease) {
				continue
			}
			if release.version == "" {
				return Release{}, errors.New("provider returned an empty release version")
			}
			asset, err := acquisitionSelectAsset(release.assets, p.Asset)
			if err != nil {
				return Release{}, err
			}
			return Release{Version: release.version, ID: release.id, Asset: asset}, nil
		}
		if p.Pin != "" || len(releases) < 100 {
			break
		}
	}
	return Release{}, errors.New("no eligible release found; check channel or exact version pin")
}

func acquisitionDecode(data []byte, provider string, pinned bool) ([]acquisitionRelease, error) {
	var result []acquisitionRelease
	if provider == "github" {
		var releases []acquisitionGitHubRelease
		if pinned {
			var release acquisitionGitHubRelease
			if json.Unmarshal(data, &release) != nil {
				return nil, errors.New("invalid GitHub release metadata")
			}
			releases = []acquisitionGitHubRelease{release}
		} else if json.Unmarshal(data, &releases) != nil {
			return nil, errors.New("invalid GitHub release metadata")
		}
		for _, r := range releases {
			release := acquisitionRelease{version: r.Tag, id: strconv.FormatInt(r.ID, 10), published: r.Published, prerelease: r.Prerelease, draft: r.Draft}
			for _, a := range r.Assets {
				digest := ""
				if a.Digest != "" {
					if !strings.HasPrefix(a.Digest, "sha256:") {
						return nil, errors.New("unsupported release asset digest")
					}
					digest = strings.TrimPrefix(a.Digest, "sha256:")
					if !acquisitionDigest(digest) {
						return nil, errors.New("invalid release asset digest")
					}
				}
				release.assets = append(release.assets, Asset{Name: a.Name, URL: a.URL, SHA256: strings.ToLower(digest)})
			}
			result = append(result, release)
		}
	} else {
		var releases []acquisitionGitLabRelease
		if pinned {
			var release acquisitionGitLabRelease
			if json.Unmarshal(data, &release) != nil {
				return nil, errors.New("invalid GitLab release metadata")
			}
			releases = []acquisitionGitLabRelease{release}
		} else if json.Unmarshal(data, &releases) != nil {
			return nil, errors.New("invalid GitLab release metadata")
		}
		for _, r := range releases {
			release := acquisitionRelease{version: r.Tag, id: r.Tag, published: r.Released, prerelease: r.Upcoming || r.Released.After(time.Now()) || acquisitionPreTag.MatchString(r.Tag)}
			for _, a := range r.Assets.Links {
				assetURL := a.URL
				if assetURL == "" {
					assetURL = a.DirectURL
				}
				release.assets = append(release.assets, Asset{Name: a.Name, URL: assetURL})
			}
			result = append(result, release)
		}
	}
	return result, nil
}

func acquisitionSelectAsset(assets []Asset, pattern string) (Asset, error) {
	var selected []Asset
	for _, a := range assets {
		match, err := path.Match(pattern, a.Name)
		if err != nil {
			return Asset{}, errors.New("invalid asset pattern")
		}
		if match {
			selected = append(selected, a)
		}
	}
	if len(selected) == 0 {
		return Asset{}, errors.New("release has no matching asset; review the package layout")
	}
	if len(selected) != 1 {
		return Asset{}, errors.New("asset pattern is ambiguous; select one exact asset")
	}
	a := selected[0]
	if !cleanRelative(a.Name, false) || strings.Contains(a.Name, "/") {
		return Asset{}, errors.New("unsafe asset filename")
	}
	if _, err := acquisitionURL(a.URL); err != nil {
		return Asset{}, err
	}
	return a, nil
}

func (s HTTPSource) Download(ctx context.Context, p Plugin, release Release, dest string) (err error) {
	if err = ValidatePlugin(p); err != nil {
		return err
	}
	base, token, err := s.connection(p)
	if err != nil {
		return err
	}
	if release.Asset.SHA256 != "" && !acquisitionDigest(release.Asset.SHA256) {
		return errors.New("invalid release asset digest")
	}
	response, err := s.request(ctx, release.Asset.URL, base, token, "application/octet-stream")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.ContentLength > maxPluginDownload {
		return errors.New("plugin download exceeds 256 MiB")
	}
	parent, name, err := acquisitionParent(dest)
	if err != nil {
		return err
	}
	defer parent.Close()
	out, err := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		if err != nil {
			parent.Remove(name)
		}
	}()
	digest := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, digest), io.LimitReader(response.Body, maxPluginDownload+1))
	if copyErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("could not read plugin download")
	}
	if n > maxPluginDownload {
		return errors.New("plugin download exceeds 256 MiB")
	}
	if release.Asset.SHA256 != "" && !strings.EqualFold(release.Asset.SHA256, hex.EncodeToString(digest.Sum(nil))) {
		return errors.New("plugin asset SHA-256 mismatch")
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

func acquisitionDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func (s HTTPSource) connection(p Plugin) (*url.URL, string, error) {
	raw := p.Source.URL
	if raw == "" {
		if p.Source.Provider == "github" {
			raw = "https://api.github.com"
		} else {
			raw = "https://gitlab.com"
		}
	}
	base, err := acquisitionURL(raw)
	if err != nil {
		return nil, "", err
	}
	token := ""
	if p.Source.Provider == "gitlab" && p.Source.Credential != "" {
		token = s.Credentials[p.Source.Credential]
		if token == "" {
			return nil, "", errors.New("named GitLab credential is missing")
		}
		if strings.ContainsAny(token, "\r\n") {
			return nil, "", errors.New("invalid GitLab credential")
		}
	}
	return base, token, nil
}

func acquisitionURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid release URL")
	}
	local := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		local = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("release URLs require HTTPS")
	}
	return u, nil
}

func acquisitionSameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func (s HTTPSource) request(ctx context.Context, raw string, origin *url.URL, token, accept string) (*http.Response, error) {
	target, err := acquisitionURL(raw)
	if err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 2 * time.Minute}
	if s.Client != nil {
		client = *s.Client
		if client.Timeout <= 0 || client.Timeout > 2*time.Minute {
			client.Timeout = 2 * time.Minute
		}
	}
	// Never borrow browser/session cookies from a caller-supplied client.
	client.Jar = nil
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many release redirects")
		}
		if _, err := acquisitionURL(req.URL.String()); err != nil {
			return err
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("insecure release redirect")
		}
		if previousRedirect != nil {
			if err := previousRedirect(req, via); err != nil {
				return err
			}
		}
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		req.Header.Del("Referer")
		req.Header.Del("PRIVATE-TOKEN")
		req.Header.Del("JOB-TOKEN")
		if token != "" && acquisitionSameOrigin(req.URL, origin) {
			req.Header.Set("PRIVATE-TOKEN", token)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, errors.New("invalid release request")
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "source-egg")
	if token != "" && acquisitionSameOrigin(target, origin) {
		req.Header.Set("PRIVATE-TOKEN", token)
	}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("release request failed")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("release request returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

// acquisitionParent rejects symlink components before opening the staging
// parent. OpenRoot keeps later file creation confined to that parent.
func acquisitionParent(filename string) (*os.Root, string, error) {
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return nil, "", err
	}
	parent := filepath.Dir(absolute)
	current := string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(parent, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		st, err := os.Lstat(current)
		if err != nil {
			return nil, "", err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, "", errors.New("staging parent must be a real directory")
		}
	}
	root, err := os.OpenRoot(parent)
	return root, filepath.Base(absolute), err
}
