package fileserver

import (
	_ "embed"
	"html/template"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type listDirHandler struct {
	root string
}

func newListDirHandler(root string) *listDirHandler {
	return &listDirHandler{root: root}
}

func (h *listDirHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Use original URL path for the trailing-slash check (path.Clean
	// strips it), and the cleaned path for filesystem access.
	origPath := r.URL.Path
	if !strings.HasPrefix(origPath, "/") {
		origPath = "/" + origPath
	}
	cleanPath := path.Clean(origPath)

	relPath := strings.TrimPrefix(cleanPath, "/")
	absPath := filepath.Join(h.root, relPath)

	rootClean := filepath.Clean(h.root)
	if absPath != rootClean && !strings.HasPrefix(absPath, rootClean+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if info.IsDir() {
		if !strings.HasSuffix(origPath, "/") {
			// Use relative redirect so browser resolves against the
			// original URL (including any StripPrefix prefix).
			redirect := path.Base(cleanPath) + "/"
			if q := r.URL.RawQuery; q != "" {
				redirect += "?" + q
			}
			http.Redirect(w, r, redirect, http.StatusMovedPermanently)
			return
		}
		h.serveDirList(w, r, cleanPath, absPath)
		return
	}

	http.ServeFile(w, r, absPath)
}

type dirItem struct {
	Name string
	Size string
}

func (h *listDirHandler) serveDirList(w http.ResponseWriter, r *http.Request, upath, absPath string) {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		http.Error(w, "Error reading directory", http.StatusInternalServerError)
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	var items []dirItem
	if upath != "/" {
		items = append(items, dirItem{Name: "../"})
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
			items = append(items, dirItem{Name: name})
		} else {
			info, ierr := e.Info()
			size := "-"
			if ierr == nil {
				size = formatSize(info.Size())
			}
			items = append(items, dirItem{Name: name, Size: size})
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = dirListTmpl.Execute(w, map[string]any{
		"Path":  upath,
		"Items": items,
	})
	if err != nil {
		http.Error(w, "Error rendering template", http.StatusInternalServerError)
	}
}

func formatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10)
	}
	div, exp := int64(unit), 0
	units := []string{"K", "M", "G", "T"}
	for n >= div*unit && exp < len(units)-1 {
		div *= unit
		exp++
	}
	return strconv.FormatInt(n/div, 10) + units[exp]
}

//go:embed listdir.html
var dirListContent string

var dirListTmpl = template.Must(template.New("dirlist").Parse(dirListContent))
