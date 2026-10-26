package egg

import (
	"errors"
	"os"
	"regexp"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/passwords"
)

var cachedCredential = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// A disabled node may retain an existing assignment, but it cannot authorize
// creating a credential. ReadJSON rejects symlinks, non-regular files and path
// escapes. An expired NextAt is intentional here: rotation is disabled.
func readCachedPassword(root *os.Root) (*passwords.Assignment, error) {
	var assignment passwords.Assignment
	if err := fsx.ReadJSON(root, ".source/password.json", &assignment); err != nil {
		return nil, errors.New("node password generation is disabled and no valid private cached assignment is available")
	}
	if len(assignment.Password) > 271 || !cachedCredential.MatchString(assignment.Password) || len(assignment.Revision) > 128 || !cachedCredential.MatchString(assignment.Revision) || assignment.GeneratedAt.IsZero() || (!assignment.NextAt.IsZero() && assignment.NextAt.Before(assignment.GeneratedAt)) {
		return nil, errors.New("node password generation is disabled and cached assignment is malformed")
	}
	return &assignment, nil
}
