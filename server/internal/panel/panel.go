// Package panel встраивает собранную веб-панель (panel/dist) в бинарь сервера
// и раздаёт её как SPA (fallback на index.html для клиентских маршрутов).
//
// В dev-режиме (panel не собрана) возвращается заглушка со ссылкой на vite dev server.
package panel

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler возвращает http.Handler, раздающий встроенную панель.
// Если dist пуст/нет index.html (dev-сборка без панели) — handler возвращает подсказку.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.HandlerFunc(devStub)
	}
	// Если index.html отсутствует — панель не собрана (dev-режим).
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return http.HandlerFunc(devStub)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		// Если файла нет — отдаём index.html (SPA routing).
		if path != "" {
			if _, err := fs.Stat(sub, path); err != nil {
				r2 := new(http.Request)
				*r2 = *r
				r2.URL.Path = "/"
				fileServer.ServeHTTP(w, r2)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

// devStub — ответ, когда панель не встроена (dev-режим).
func devStub(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Spider — dev</title>
<body style="font-family:system-ui;background:#0b0d10;color:#cdd5e0;padding:3rem;line-height:1.6">
<h1>Панель не встроена</h1>
<p>Сервер запущен без встроенной панели. В режиме разработки запустите её отдельно:</p>
<pre style="background:#15181d;padding:1rem;border-radius:8px">cd panel &amp;&amp; npm run dev</pre>
<p>И откройте <a style="color:#7dd3fc" href="http://localhost:5173">http://localhost:5173</a>.</p>
<p>Для prod: <code>npm run build</code> в <code>panel/</code> — dist встроится в бинарь.</p>
</body>`))
}
