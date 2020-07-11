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
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var serverMutex sync.Mutex
var serverStarted bool
var server http.Server

var writerMap = make(map[string]*writer)
var writerMapLock = new(sync.Mutex)
var upgrader = websocket.Upgrader{}
var stopGC = make(chan bool)

var textTemplate *template.Template
var mainTemplate *template.Template

var dsgvo []byte
var impressum []byte

var cachedFiles = make(map[string][]byte)
var etagCompare string

var robottxt = []byte(`User-agent: *
Disallow: /`)

func init() {
	upgrader.HandshakeTimeout = 30 * time.Second

	b, err := ioutil.ReadFile("template/text.html")
	if err != nil {
		panic(err)
	}

	textTemplate, err = template.New("text").Parse(string(b))
	if err != nil {
		panic(err)
	}

	b, err = ioutil.ReadFile("template/main.html")
	if err != nil {
		panic(err)
	}

	mainTemplate, err = template.New("main").Parse(string(b))
	if err != nil {
		panic(err)
	}
}

type textTemplateStruct struct {
	Text        template.HTML
	Translation Translation
}

type mainTemplateStruct struct {
	SyncTime    int
	Translation Translation
}

func initialiseServer() error {
	if serverStarted {
		return nil
	}
	server = http.Server{Addr: config.Address}

	// Do setup
	// DSGVO
	b, err := ioutil.ReadFile(config.PathDSGVO)
	if err != nil {
		return err
	}
	text := textTemplateStruct{Format(b), GetDefaultTranslation()}
	output := bytes.NewBuffer(make([]byte, 0, len(text.Text)*2))
	textTemplate.Execute(output, text)
	dsgvo = output.Bytes()

	http.HandleFunc("/dsgvo.html", func(rw http.ResponseWriter, r *http.Request) {
		rw.Write(dsgvo)
	})

	// Impressum
	b, err = ioutil.ReadFile(config.PathImpressum)
	if err != nil {
		return err
	}
	text = textTemplateStruct{Format(b), GetDefaultTranslation()}
	output = bytes.NewBuffer(make([]byte, 0, len(text.Text)*2))
	textTemplate.Execute(output, text)
	impressum = output.Bytes()
	http.HandleFunc("/impressum.html", func(rw http.ResponseWriter, r *http.Request) {
		rw.Write(impressum)
	})

	// static files
	for _, d := range []string{"css/", "static/", "font/", "js/"} {
		filepath.Walk(d, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				log.Panicln("server: Error wile caching files:", err)
			}

			if info.Mode().IsRegular() {
				log.Println("static file handler: Caching file", path)

				b, err := ioutil.ReadFile(path)
				if err != nil {
					log.Println("static file handler: Error reading file:", err)
					return err
				}
				cachedFiles[path] = b
				return nil
			}
			return nil
		})
	}

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
		path = strings.TrimPrefix(path, "/")
		data, ok := cachedFiles[path]
		if !ok {
			rw.WriteHeader(http.StatusNotFound)
		} else {
			rw.Header().Set("ETag", etag)
			rw.Header().Set("Cache-Control", "public, max-age=43200")
			switch {
			case strings.HasSuffix(path, ".svg"):
				rw.Header().Set("Content-Type", "image/svg+xml")
			case strings.HasSuffix(path, ".css"):
				rw.Header().Set("Content-Type", "text/css")
			case strings.HasSuffix(path, ".ttf"):
				rw.Header().Set("Content-Type", "application/x-font-truetype")
			case strings.HasSuffix(path, ".js"):
				rw.Header().Set("Content-Type", "application/javascript")
			default:
				rw.Header().Set("Content-Type", "text/plain")
			}
			rw.Write(data)
		}
	}

	http.HandleFunc("/css/", staticHandle)
	http.HandleFunc("/static/", staticHandle)
	http.HandleFunc("/font/", staticHandle)
	http.HandleFunc("/js/", staticHandle)

	http.HandleFunc("/favicon.ico", func(rw http.ResponseWriter, r *http.Request) {
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

		f, ok := cachedFiles["static/favicon.ico"]

		if !ok {
			rw.WriteHeader(http.StatusNotFound)
			return
		}

		rw.Write(f)
	})

	// robots.txt
	http.HandleFunc("/robots.txt", func(rw http.ResponseWriter, r *http.Request) {
		rw.Write(robottxt)
	})

	http.HandleFunc("/", rootHandle)
	return nil
}

func rootHandle(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		// redirect too random ressource
		target := strings.Join([]string{"/", url.PathEscape(RandomString())}, "")
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
