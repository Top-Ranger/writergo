// SPDX-License-Identifier: Apache-2.0
// Copyright 2020 Marcus Soll
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var serverMutex sync.Mutex
var serverStarted bool
var server http.Server
var rootPath string

var writerMap = make(map[string]*writer)
var writerMapLock = new(sync.Mutex)
var upgrader = websocket.Upgrader{}
var stopGC = make(chan bool)

//go:embed template
var templateFiles embed.FS

var textTemplate *template.Template
var mainTemplate *template.Template

var dsgvo []byte
var impressum []byte

//go:embed static font js css
var cachedFiles embed.FS
var etagCompare string
var cssTemplates *template.Template

var robottxt = []byte(`User-agent: *
Disallow: /`)

func init() {
	upgrader.HandshakeTimeout = 30 * time.Second

	var err error

	textTemplate, err = template.ParseFS(templateFiles, "template/text.html")
	if err != nil {
		panic(err)
	}

	mainTemplate, err = template.ParseFS(templateFiles, "template/main.html")
	if err != nil {
		panic(err)
	}

	cssTemplates, err = template.ParseFS(cachedFiles, "css/*")
	if err != nil {
		panic(err)
	}
}

type textTemplateStruct struct {
	Text        template.HTML
	Translation Translation
	ServerPath  string
}

type mainTemplateStruct struct {
	SyncTime    int
	Translation Translation
	ServerPath  string
}

func initialiseServer() error {
	if serverStarted {
		return nil
	}
	server = http.Server{Addr: config.Address}

	// Do setup
	rootPath = strings.Join([]string{config.ServerPath, "/"}, "")

	// DSGVO
	b, err := os.ReadFile(config.PathDSGVO)
	if err != nil {
		return err
	}
	text := textTemplateStruct{Format(b), GetDefaultTranslation(), config.ServerPath}
	output := bytes.NewBuffer(make([]byte, 0, len(text.Text)*2))
	textTemplate.Execute(output, text)
	dsgvo = output.Bytes()

	http.HandleFunc(strings.Join([]string{config.ServerPath, "/dsgvo.html"}, ""), func(rw http.ResponseWriter, r *http.Request) {
		rw.Write(dsgvo)
	})

	// Impressum
	b, err = os.ReadFile(config.PathImpressum)
	if err != nil {
		return err
	}
	text = textTemplateStruct{Format(b), GetDefaultTranslation(), config.ServerPath}
	output = bytes.NewBuffer(make([]byte, 0, len(text.Text)*2))
	textTemplate.Execute(output, text)
	impressum = output.Bytes()
	http.HandleFunc(strings.Join([]string{config.ServerPath, "/impressum.html"}, ""), func(rw http.ResponseWriter, r *http.Request) {
		rw.Write(impressum)
	})

	etag := fmt.Sprint("\"", strconv.FormatInt(time.Now().Unix(), 10), "\"")
	etagCompare := strings.TrimSuffix(etag, "\"")
	etagCompareApache := strings.Join([]string{etagCompare, "-"}, "")       // Dirty hack for apache2, who appends -gzip inside the quotes if the file is compressed, thus preventing If-None-Match matching the ETag
	etagCompareCaddy := strings.Join([]string{"W/", etagCompare, "\""}, "") // Dirty hack for caddy, who appends W/ before the quotes if the file is compressed, thus preventing If-None-Match matching the ETag

	staticHandle := func(rw http.ResponseWriter, r *http.Request) {
		// Check for ETag
		v, ok := r.Header["If-None-Match"]
		if ok {
			for i := range v {
				if v[i] == etag || v[i] == etagCompareCaddy || strings.HasPrefix(v[i], etagCompareApache) {
					rw.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		// Send file if existing in cache
		path := r.URL.Path
		path = strings.TrimPrefix(path, config.ServerPath)
		path = strings.TrimPrefix(path, "/")

		if strings.HasPrefix(path, "css/") {
			// special case
			path = strings.TrimPrefix(path, "css/")
			rw.Header().Set("Content-Type", "text/css")
			err := cssTemplates.ExecuteTemplate(rw, path, struct{ ServerPath string }{config.ServerPath})
			if err != nil {
				log.Println("server:", err)
			}
			return
		}

		data, err := cachedFiles.Open(path)
		if err != nil {
			rw.WriteHeader(http.StatusNotFound)
		} else {
			rw.Header().Set("ETag", etag)
			rw.Header().Set("Cache-Control", "public, max-age=43200")
			switch {
			case strings.HasSuffix(path, ".svg"):
				rw.Header().Set("Content-Type", "image/svg+xml")
			case strings.HasSuffix(path, ".ttf"):
				rw.Header().Set("Content-Type", "application/x-font-truetype")
			case strings.HasSuffix(path, ".js"):
				rw.Header().Set("Content-Type", "application/javascript")
			default:
				rw.Header().Set("Content-Type", "text/plain")
			}
			io.Copy(rw, data)
		}
	}

	http.HandleFunc(strings.Join([]string{config.ServerPath, "/css/"}, ""), staticHandle)
	http.HandleFunc(strings.Join([]string{config.ServerPath, "/static/"}, ""), staticHandle)
	http.HandleFunc(strings.Join([]string{config.ServerPath, "/font/"}, ""), staticHandle)
	http.HandleFunc(strings.Join([]string{config.ServerPath, "/js/"}, ""), staticHandle)

	http.HandleFunc(strings.Join([]string{config.ServerPath, "/favicon.ico"}, ""), func(rw http.ResponseWriter, r *http.Request) {
		// Check for ETag
		v, ok := r.Header["If-None-Match"]
		if ok {
			for i := range v {
				if v[i] == etag || v[i] == etagCompareCaddy || strings.HasPrefix(v[i], etagCompareApache) {
					rw.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		f, err := cachedFiles.ReadFile("static/favicon.ico")

		if err != nil {
			rw.WriteHeader(http.StatusNotFound)
			return
		}

		rw.Write(f)
	})

	// robots.txt
	http.HandleFunc(strings.Join([]string{config.ServerPath, "/robots.txt"}, ""), func(rw http.ResponseWriter, r *http.Request) {
		rw.Write(robottxt)
	})

	http.HandleFunc("/", rootHandle)
	return nil
}

func rootHandle(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path == rootPath || r.URL.Path == config.ServerPath || r.URL.Path == "/" {
		// redirect too random ressource
		target := strings.Join([]string{config.ServerPath, "/", url.PathEscape(RandomString())}, "")
		http.Redirect(rw, r, target, http.StatusSeeOther)
		return
	}

	key := r.URL.Path
	key = strings.TrimLeft(key, "/")

	ws := r.URL.Query().Get("ws")

	if ws != "" {
		// Upgrade connection and add to writer
		conn, err := upgrader.Upgrade(rw, r, nil)
		if err != nil {
			log.Println("upgrade:", err)
			return
		}

		writerMapLock.Lock()
		defer writerMapLock.Unlock()

		w := writerMap[key]
		if w == nil {
			w = new(writer)
			w.Key = key
			writerMap[key] = w
		}

		err = w.AddNew(conn)
		if err != nil {
			log.Println(key, "add connection:", err)
		}
		return
	}

	td := mainTemplateStruct{
		SyncTime:    config.SyncSeconds * 1000,
		Translation: GetDefaultTranslation(),
		ServerPath:  config.ServerPath,
	}
	err := mainTemplate.Execute(rw, td)
	if err != nil {
		log.Println("main template:", err)
	}
}

// RunServer starts the actual server.
// It does nothing if a server is already started.
// It will return directly after the server is started.
func RunServer() {
	serverMutex.Lock()
	defer serverMutex.Unlock()
	if serverStarted {
		return
	}

	err := initialiseServer()
	if err != nil {
		log.Panicln("server:", err)
	}
	log.Println("server: Server starting at", config.Address)
	serverStarted = true
	go func() {
		err = server.ListenAndServe()
		if err != http.ErrServerClosed {
			log.Println("server:", err)
		}
	}()
	go serverGCWorker()
}

// StopServer shuts the server down.
// It will do nothing if the server is not started.
// It will return after the shutdown is completed.
func StopServer() {
	serverMutex.Lock()
	defer serverMutex.Unlock()
	if !serverStarted {
		return
	}
	err := server.Shutdown(context.Background())
	if err == nil {
		log.Println("server: stopped")
	} else {
		log.Println("server:", err)
	}
	stopGC <- true
}

func serverGCWorker() {
	if config.GCMinutes <= 0 {
		// no gc
		return
	}

	log.Println("gc:", "worker started")

	for {
		t := time.NewTicker(time.Duration(config.GCMinutes) * time.Minute)
		select {
		case <-stopGC:
			return
		case <-t.C:
			writerMapLock.Lock()
			log.Println("gc:", "begin gc")
			for k := range writerMap {
				if writerMap[k].CanBeDeleted() {
					log.Println("gc:", "removed", k)
					delete(writerMap, k)
				}
			}
			log.Println("gc:", "finished gc")
			writerMapLock.Unlock()
		}
	}
}
