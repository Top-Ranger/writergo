// SPDX-License-Identifier: Apache-2.0
// Copyright 2020,2022 Marcus Soll
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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	_ "github.com/Top-Ranger/writergo/datasafe"
	"github.com/Top-Ranger/writergo/registry"
)

// ConfigStruct contains all configuration options for PollGo!
type ConfigStruct struct {
	Language       string
	Address        string
	PathImpressum  string
	PathDSGVO      string
	SyncSeconds    int
	GCMinutes      int
	ServerPath     string
	DataSafe       string
	DataSafeConfig string
}

var config ConfigStruct
var ds registry.DataSafe

func loadConfig(path string) (ConfigStruct, error) {
	log.Printf("main: Loading config (%s)", path)
	b, err := os.ReadFile(path)
	if err != nil {
		return ConfigStruct{}, errors.New(fmt.Sprintln("Can not read config.json:", err))
	}

	c := ConfigStruct{}
	err = json.Unmarshal(b, &c)
	if err != nil {
		return ConfigStruct{}, errors.New(fmt.Sprintln("Error while parsing config.json:", err))
	}

	if !strings.HasPrefix(c.ServerPath, "/") && c.ServerPath != "" {
		log.Println("load config: ServerPath does not start with '/', adding it as a prefix")
		c.ServerPath = strings.Join([]string{"/", c.ServerPath}, "")
	}
	c.ServerPath = strings.TrimSuffix(c.ServerPath, "/")

	return c, nil
}

func printInfo() {
	log.Println("WriterGo!")
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		log.Print("- no build info found")
		return
	}

	log.Printf("- go version: %s", bi.GoVersion)
	for _, s := range bi.Settings {
		switch s.Key {
		case "-tags":
			log.Printf("- build tags: %s", s.Value)
		case "vcs.revision":
			l := 7
			if len(s.Value) > 7 {
				s.Value = s.Value[:l]
			}
			log.Printf("- commit: %s", s.Value)
		case "vcs.modified":
			log.Printf("- files modified: %s", s.Value)
		}
	}
}

func main() {
	printInfo()

	configPath := flag.String("config", "./config.json", "Path to json config for WriterGo!")
	flag.Parse()

	c, err := loadConfig(*configPath)
	if err != nil {
		panic(err)
	}
	config = c

	log.Printf("main: Using DataSafe '%s'", config.DataSafe)
	var found bool
	ds, found = registry.GetDataSafe(config.DataSafe)
	if !found {
		log.Panicln("unknown data safe", config.DataSafe)
	}

	err = ds.LoadConfig([]byte(config.DataSafeConfig))
	if err != nil {
		panic(err)
	}

	err = SetDefaultTranslation(config.Language)
	if err != nil {
		log.Panicf("main: Error setting default language '%s': %s", config.Language, err.Error())
	}
	log.Printf("main: Setting language to '%s'", config.Language)

	RunServer()

	s := make(chan os.Signal, 1)
	signal.Notify(s, os.Interrupt, syscall.SIGTERM)

	log.Println("main: waiting")

	for range s {
		StopServer()
		ds.FlushAndClose()
		return
	}
}
