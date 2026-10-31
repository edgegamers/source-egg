package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sourceRecipeJSON = `{"id":"battleground","source":{"provider":"gitlab","url":"https://gitlab.example.org","repository":"source/plugins/battleground","credential":"gitlab","ref":"main"},"build":{"sourcemod":"1.13.0.7478","sources":["scripting/*.sp"],"include_dirs":["scripting/include"],"dependencies":[{"source":{"provider":"github","repository":"Bara/Multi-Colors","ref":"master"},"include_dirs":["addons/sourcemod/scripting/include"]}]},"mappings":[{"from":"compiled","to":"addons/sourcemod/plugins"}]}`

func sourceRecipe(t *testing.T) Plugin {
	t.Helper()
	var p Plugin
	d := json.NewDecoder(strings.NewReader(sourceRecipeJSON))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHTTPSourceRepositoryCommitsAndDependencyIdentity(t *testing.T) {
	primary := strings.Repeat("a", 40)
	dependency := strings.Repeat("b", 40)
	var origin string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/source%2Fplugins%2Fbattleground/repository/commits/feature%2Fnext":
			if r.Header.Get("PRIVATE-TOKEN") != "private-secret" {
				t.Error("GitLab credential missing")
			}
			json.NewEncoder(w).Encode(map[string]string{"id": primary})
		case "/repos/Bara/Multi-Colors/commits/master":
			if r.Header.Get("PRIVATE-TOKEN") != "" {
				t.Error("GitHub dependency received GitLab credential")
			}
			json.NewEncoder(w).Encode(map[string]string{"sha": dependency})
		case "/api/v4/projects/source%2Fplugins%2Fbattleground/repository/archive.tar.gz":
			if r.URL.Query().Get("sha") != primary {
				t.Error("archive downloaded a moving branch")
			}
			w.Write([]byte("source snapshot"))
		default:
			t.Errorf("unexpected source request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	origin = srv.URL
	p := sourceRecipe(t)
	p.Source.URL, p.Source.Ref = origin, "feature/next"
	p.Build.Dependencies[0].Source.URL = origin
	source := HTTPSource{Client: srv.Client(), Credentials: map[string]string{"gitlab": "private-secret"}}
	r, err := source.Resolve(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(r.Asset.URL)
	if u.Query().Get("sha") != primary || len(r.Dependencies) != 1 || r.Dependencies[0].Commit != dependency || !strings.Contains(r.Version, primary) {
		t.Fatalf("unbound source revisions: %#v", r)
	}
	dest := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := source.Download(context.Background(), p, r, dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "source snapshot" {
		t.Fatalf("wrong source download: %q", b)
	}
	dependency = strings.Repeat("c", 40)
	next, err := source.Resolve(context.Background(), p)
	if err != nil || next.Version == r.Version {
		t.Fatalf("dependency-only update invisible: %#v %v", next, err)
	}
}

func TestHTTPSourceRepositoryRejectsMalformedCommit(t *testing.T) {
	for _, bad := range []string{"", "main", strings.Repeat("z", 40), strings.Repeat("a", 39), "../secret"} {
		t.Run(bad, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]string{"id": bad})
			}))
			defer srv.Close()
			p := sourceRecipe(t)
			p.Source.URL = srv.URL
			p.Build.Dependencies = nil
			if _, err := (HTTPSource{Client: srv.Client(), Credentials: map[string]string{"gitlab": "secret"}}).Resolve(context.Background(), p); err == nil {
				t.Fatal("accepted invalid commit identifier")
			}
		})
	}
}

func TestSourceRecipeWithoutReleaseAsset(t *testing.T) {
	p := sourceRecipe(t)
	if err := ValidatePlugin(p); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRecipeBuildValidation(t *testing.T) {
	changes := [][2]string{
		{`"build":`, `"asset":"plugin.zip","build":`},
		{`"build":`, `"format":"tar.gz","build":`},
		{`"build":`, `"pin":"v1","build":`},
		{`"build":`, `"prerelease":true,"build":`},
		{`"sourcemod":"1.13.0.7478"`, `"sourcemod":""`},
		{`"sourcemod":"1.13.0.7478"`, `"sourcemod":"latest"`},
		{`"scripting/*.sp"`, `"../outside.sp"`},
		{`"scripting/*.sp"`, `"/outside.sp"`},
		{`"scripting/*.sp"`, `"scripting/*.sh"`},
		{`"scripting/*.sp"`, `"scripting/[.sp"`},
		{`"sources":["scripting/*.sp"]`, `"sources":[]`},
		{`"include_dirs":["scripting/include"]`, `"include_dirs":["../private"]`},
		{`"ref":"main"`, `"ref":"../../private"`},
		{`"ref":"main"`, `"ref":"main\nsecret"`},
		{`"include_dirs":["addons/sourcemod/scripting/include"]`, `"include_dirs":[]`},
		{`"provider":"github","repository":"Bara/Multi-Colors"`, `"provider":"github","repository":"Bara/Multi-Colors","credential":"gitlab"`},
	}
	for _, change := range changes {
		t.Run(change[1], func(t *testing.T) {
			var p Plugin
			if err := json.Unmarshal([]byte(strings.Replace(sourceRecipeJSON, change[0], change[1], 1)), &p); err != nil {
				t.Fatal(err)
			}
			if ValidatePlugin(p) == nil {
				t.Fatal("accepted invalid build recipe")
			}
		})
	}
}

func TestSourceRecipeFieldsDoNotChangeReleaseHashes(t *testing.T) {
	p := acquisitionPlugin()
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"example","source":{"provider":"github","repository":"owner/repo"},"asset":"plugin-*.zip","format":"zip","mappings":[{"from":"addons","to":"addons"}]}`
	if string(encoded) != want {
		t.Fatalf("existing release recipe serialization changed: %s", encoded)
	}
}

func TestSourceManifestExample(t *testing.T) {
	data, err := os.ReadFile("../../docs/examples/plugins-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Plugins) != 3 {
		t.Fatal("source example lost its three repository/layout examples")
	}
}
