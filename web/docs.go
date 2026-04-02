package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const docsDir = "docs"

type docItem struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

func HandleDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{"docs": []docItem{}})
		return
	}
	var docs []docItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		title := strings.TrimSuffix(e.Name(), ".md")
		title = strings.ReplaceAll(title, "-", " ")
		title = strings.ReplaceAll(title, "_", " ")
		docs = append(docs, docItem{Name: e.Name(), Title: title})
	}
	if docs == nil {
		docs = []docItem{}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"docs": docs})
}

func HandleDoc(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/doc/")
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "/") {
		http.Error(w, "invalid name", 400)
		return
	}
	data, err := os.ReadFile(filepath.Join(docsDir, name))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "文档不存在"})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}
