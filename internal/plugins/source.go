package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
)

func (s HTTPSource) resolveBuild(ctx context.Context, p Plugin) (Release, error) {
	primary, err := s.resolveRevision(ctx, p.Source)
	if err != nil {
		return Release{}, err
	}
	result := Release{Asset: primary.Asset}
	identity := []string{p.Build.SourceMod, primary.Commit}
	for _, dep := range p.Build.Dependencies {
		revision, err := s.resolveRevision(ctx, dep.Source)
		if err != nil {
			return Release{}, errors.New("include dependency lookup failed: " + err.Error())
		}
		result.Dependencies = append(result.Dependencies, revision)
		identity = append(identity, revision.Commit)
	}
	data, _ := json.Marshal(identity)
	sum := sha256.Sum256(data)
	result.Version = primary.Commit + "-" + hex.EncodeToString(sum[:])[:12]
	result.ID = result.Version
	return result, nil
}

func (s HTTPSource) resolveRevision(ctx context.Context, source Source) (Revision, error) {
	if err := validateSource(source); err != nil {
		return Revision{}, err
	}
	base, token, err := s.connection(Plugin{Source: source})
	if err != nil {
		return Revision{}, err
	}
	ref := source.Ref
	if ref == "" {
		ref = "main"
	}
	prefix := strings.TrimRight(base.String(), "/")
	var endpoint string
	if source.Provider == "github" {
		prefix += "/repos/" + source.Repository
		endpoint = prefix + "/commits/" + url.PathEscape(ref)
	} else {
		prefix += "/api/v4/projects/" + url.PathEscape(source.Repository) + "/repository"
		endpoint = prefix + "/commits/" + url.PathEscape(ref)
	}
	response, err := s.request(ctx, endpoint, base, token, "application/json")
	if err != nil {
		return Revision{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseMetadata+1))
	if err != nil || len(data) > int(maxReleaseMetadata) {
		return Revision{}, errors.New("could not read bounded repository metadata")
	}
	var commit struct {
		ID  string `json:"id"`
		SHA string `json:"sha"`
	}
	if json.Unmarshal(data, &commit) != nil {
		return Revision{}, errors.New("invalid repository metadata")
	}
	sha := commit.ID
	if source.Provider == "github" {
		sha = commit.SHA
	}
	if !validCommit(sha) {
		return Revision{}, errors.New("provider returned an invalid full commit ID")
	}
	archiveURL := prefix + "/archive.tar.gz?sha=" + sha
	if source.Provider == "github" {
		archiveURL = prefix + "/tarball/" + sha
	}
	return Revision{Commit: sha, Asset: Asset{Name: "source.tar.gz", URL: archiveURL}}, nil
}

func validCommit(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	for _, c := range sha {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
