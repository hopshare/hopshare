package migrations

import (
	"embed"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed *.sql
var files embed.FS

// Migration is a single SQL migration.
type Migration struct {
	Version string
	SQL     string
}

// List returns migrations sorted by version derived from filename (without extension).
func List() ([]Migration, error) {
	paths, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, err
	}

	sort.Strings(paths)

	migs := make([]Migration, 0, len(paths))
	for _, p := range paths {
		content, err := fs.ReadFile(files, p)
		if err != nil {
			return nil, err
		}
		version := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		migs = append(migs, Migration{
			Version: version,
			SQL:     string(content),
		})
	}

	return migs, nil
}
