package plg_viewer_dwg

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	. "github.com/mickael-kerjean/filestash/server/common"
	"github.com/mickael-kerjean/filestash/server/middleware"
	"github.com/mickael-kerjean/filestash/server/model"

	"github.com/gorilla/mux"
	"github.com/patrickmn/go-cache"
)

var (
	dwg_cache     *cache.Cache
	plugin_enable func() bool
)

type dwgCacheData struct {
	Path string
	Cat  func(path string) (io.ReadCloser, error)
}

func init() {
	dwg_cache = cache.New(30*time.Minute, 30*time.Minute)

	plugin_enable = func() bool {
		return Config.Get("features.dwg_viewer.enable").Schema(func(f *FormElement) *FormElement {
			if f == nil {
				f = &FormElement{}
			}
			f.Name = "enable"
			f.Type = "enable"
			f.Description = "Enable/Disable DWG/DXF file viewer (requires LibreDWG to be installed)"
			f.Default = false
			return f
		}).Bool()
	}

	Hooks.Register.Onload(func() {
		plugin_enable()
	})

	Hooks.Register.HttpEndpoint(func(r *mux.Router, app *App) error {
		r.HandleFunc(
			COOKIE_PATH+"dwg/viewer",
			middleware.NewMiddlewareChain(
				ViewerHandler,
				[]Middleware{middleware.SessionStart, middleware.LoggedInOnly},
				*app,
			),
		).Methods("GET")

		r.HandleFunc(
			"/dwg/render",
			RenderHandler,
		).Methods("GET")

		return nil
	})

	// Registruje DWG i DXF MIME tipove za ovaj viewer
	Hooks.Register.XDGOpen(`
        if (mime === "image/vnd.dwg" || mime === "image/vnd.dxf" ||
            mime === "application/acad" || mime === "application/x-acad" ||
            mime === "application/dwg" || mime === "application/dxf") {
            return ["appframe", {"endpoint": "/api/dwg/viewer"}];
        }
    `)
}

// ViewerHandler - vraća HTML stranicu sa SVG prikazom DWG fajla
func ViewerHandler(ctx *App, res http.ResponseWriter, req *http.Request) {
	if plugin_enable() == false {
		res.WriteHeader(http.StatusServiceUnavailable)
		res.Write([]byte("<p>DWG viewer is disabled</p>"))
		return
	}
	if model.CanRead(ctx) == false {
		SendErrorResult(res, ErrPermissionDenied)
		return
	}

	path := req.URL.Query().Get("path")
	if path == "" {
		SendErrorResult(res, NewError("missing path parameter", http.StatusBadRequest))
		return
	}

	key := Hash(fmt.Sprintf("%s-%d", path, time.Now().UnixNano()), 20)
	dwg_cache.Set(key, &dwgCacheData{path, ctx.Backend.Cat}, cache.DefaultExpiration)

	filename := filepath.Base(path)

	tmpl, err := template.New("dwg_viewer").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{{ .filename }}</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { background: #1e1e1e; color: #ccc; font-family: monospace; height: 100vh; display: flex; flex-direction: column; }
        #toolbar { padding: 8px 12px; background: #2d2d2d; display: flex; align-items: center; gap: 12px; border-bottom: 1px solid #444; }
        #toolbar span { font-size: 13px; opacity: 0.7; }
        #controls { display: flex; gap: 6px; }
        button { background: #444; color: #ccc; border: none; padding: 4px 10px; cursor: pointer; border-radius: 3px; font-size: 12px; }
        button:hover { background: #555; }
        #viewport { flex: 1; overflow: auto; display: flex; align-items: center; justify-content: center; }
        #svg-container { transform-origin: center center; transition: transform 0.1s; }
        #svg-container svg { max-width: 100%; display: block; }
        #loading { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); font-size: 16px; opacity: 0.6; }
        #error { color: #e74c3c; text-align: center; padding: 20px; }
    </style>
</head>
<body>
    <div id="toolbar">
        <span>{{ .filename }}</span>
        <div id="controls">
            <button onclick="zoom(1.2)">+</button>
            <button onclick="zoom(0.8)">-</button>
            <button onclick="resetZoom()">Reset</button>
        </div>
    </div>
    <div id="viewport">
        <div id="loading">Converting DWG file...</div>
        <div id="svg-container"></div>
        <div id="error" style="display:none"></div>
    </div>

    <script>
        var scale = 1;
        var container = document.getElementById('svg-container');

        function zoom(factor) {
            scale *= factor;
            container.style.transform = 'scale(' + scale + ')';
        }

        function resetZoom() {
            scale = 1;
            container.style.transform = 'scale(1)';
        }

        // Učitaj SVG
        fetch('/dwg/render?key={{ .key }}')
            .then(function(r) {
                if (!r.ok) return r.text().then(function(t) { throw new Error(t); });
                return r.text();
            })
            .then(function(svg) {
                document.getElementById('loading').style.display = 'none';
                container.innerHTML = svg;
                // Prilagodi boje za tamnu pozadinu
                var svgEl = container.querySelector('svg');
                if (svgEl) {
                    svgEl.style.background = '#fff';
                    svgEl.style.maxHeight = '80vh';
                }
            })
            .catch(function(err) {
                document.getElementById('loading').style.display = 'none';
                document.getElementById('error').style.display = 'block';
                document.getElementById('error').textContent = 'Error: ' + err.message;
            });
    </script>
</body>
</html>
`)
	if err != nil {
		SendErrorResult(res, err)
		return
	}

	res.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(res, map[string]interface{}{
		"filename": filename,
		"key":      key,
	})
}

// RenderHandler - konvertuje DWG u SVG i vraća ga
func RenderHandler(res http.ResponseWriter, req *http.Request) {
	key := req.URL.Query().Get("key")
	if key == "" {
		http.Error(res, "missing key", http.StatusBadRequest)
		return
	}

	cached, found := dwg_cache.Get(key)
	if !found {
		http.Error(res, "session expired, please reload", http.StatusGone)
		return
	}
	data, ok := cached.(*dwgCacheData)
	if !ok {
		http.Error(res, "invalid cache data", http.StatusInternalServerError)
		return
	}

	// Preuzmi fajl iz backend-a
	reader, err := data.Cat(data.Path)
	if err != nil {
		http.Error(res, fmt.Sprintf("cannot read file: %v", err), http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	// Napravi privremeni direktorijum
	tmpDir, err := os.MkdirTemp("", "dwg_viewer_*")
	if err != nil {
		http.Error(res, "cannot create temp dir", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	// Sačuvaj ulazni fajl
	ext := strings.ToLower(filepath.Ext(data.Path))
	if ext == "" {
		ext = ".dwg"
	}
	inputPath := filepath.Join(tmpDir, "input"+ext)
	inputFile, err := os.Create(inputPath)
	if err != nil {
		http.Error(res, "cannot create temp file", http.StatusInternalServerError)
		return
	}
	if _, err = io.Copy(inputFile, reader); err != nil {
		inputFile.Close()
		http.Error(res, "cannot write temp file", http.StatusInternalServerError)
		return
	}
	inputFile.Close()

	// Konvertuj DWG → SVG koristeći LibreDWG
	// dwg2svg generira fajl sa istim imenom u istom direktorijumu
	var outputPath string

	if ext == ".dxf" {
		// Za DXF fajlove koristimo dxf2svg ako postoji, inače dwg2svg direktno
		outputPath = strings.TrimSuffix(inputPath, ext) + ".svg"
		cmd := exec.Command("dwg2svg", inputPath)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			Log.Warning("[dwg_viewer] dwg2svg failed: %s, output: %s", err.Error(), string(out))
			http.Error(res, fmt.Sprintf("conversion failed: %v\n%s", err, string(out)), http.StatusInternalServerError)
			return
		}
	} else {
		// Za DWG: dwg2svg <input.dwg> → generira <input.svg> u istom direktorijumu
		outputPath = filepath.Join(tmpDir, "input.svg")
		cmd := exec.Command("dwg2svg", inputPath)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			Log.Warning("[dwg_viewer] dwg2svg failed: %s, output: %s", err.Error(), string(out))
			http.Error(res, fmt.Sprintf("conversion failed: %v\n%s", err, string(out)), http.StatusInternalServerError)
			return
		}
	}

	// Pročitaj generisani SVG
	svgContent, err := os.ReadFile(outputPath)
	if err != nil {
		http.Error(res, fmt.Sprintf("cannot read SVG output: %v", err), http.StatusInternalServerError)
		return
	}

	res.Header().Set("Content-Type", "image/svg+xml")
	res.Write(svgContent)
}
