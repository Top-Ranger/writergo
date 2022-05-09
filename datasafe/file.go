// SPDX-License-Identifier: Apache-2.0
// Copyright 2022 Marcus Soll
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

package datasafe

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Top-Ranger/writergo/registry"
)

func init() {
	err := registry.RegisterDataSafe(&File{}, "File")
	if err != nil {
		panic(err)
	}
	err = registry.RegisterDataSafe(&File{}, "file")
	if err != nil {
		panic(err)
	}
}

type File struct {
	path  string
	write chan struct{ key, data string }
	read  chan struct {
		key  string
		back chan<- string
	}
	flushed chan bool
	start   sync.Once
	stop    context.CancelFunc
}

func (f *File) SaveWriter(key, data string) error {
	go func() { f.write <- struct{ key, data string }{key, data} }()
	return nil
}

func (f *File) LoadWriter(key string) (string, error) {
	back := make(chan string, 1)
	f.read <- struct {
		key  string
		back chan<- string
	}{key, back}
	return <-back, nil
}

func (f *File) LoadConfig(data []byte) error {
	run := false

	stat, err := os.Stat(string(data))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			err = os.MkdirAll(string(data), 0700)
			if err != nil {
				return fmt.Errorf("can not create path '%s': %w", string(data), err)
			}
		} else {
			return fmt.Errorf("can not check path '%s': %w", string(data), err)
		}
	} else {
		if !stat.IsDir() {
			return fmt.Errorf("path '%s' is not a directory", string(data))
		}
	}

	f.start.Do(func() {
		f.path = string(data)
		f.write = make(chan struct {
			key  string
			data string
		}, 10)
		f.read = make(chan struct {
			key  string
			back chan<- string
		}, 1)
		f.flushed = make(chan bool, 1)
		run = true
		ctx := context.Background()
		ctx, f.stop = context.WithCancel(ctx)
		go f.worker(ctx)
	})

	if !run {
		return errors.New("file data safe already loaded")
	}
	return nil
}

func (*File) IsPermanent() bool {
	return true
}

func (f *File) FlushAndClose() {
	f.stop()
	<-f.flushed
	f.write = nil
	f.read = nil
}

func (*File) generateKey(key string) string {
	key = strings.ReplaceAll(key, string(os.PathSeparator), "﷒") // U+FDD2
	key = strings.ReplaceAll(key, ".", "﷓")                      // U+FDD3
	return key
}

func (f *File) worker(ctx context.Context) {
	closer := ctx.Done()
	var closer2 <-chan time.Time
	var t *time.Timer

	for {
		select {
		case d := <-f.write:
			d.key = f.generateKey(d.key)
			func() {
				path := filepath.Join(f.path, d.key)
				f, err := os.Create(path)
				if err != nil {
					log.Println("file write: can not open file:", err)
					return
				}
				defer f.Close()
				_, err = f.WriteString(d.data)
				if err != nil {
					log.Println("file write: can not write data:", err)
					return
				}
			}()
			if t != nil {
				if !t.Stop() {
					<-t.C
				}
				t.Reset(1 * time.Second)
				closer2 = t.C
			}
		case d := <-f.read:
			d.key = f.generateKey(d.key)
			func() {
				path := filepath.Join(f.path, d.key)
				b, err := ioutil.ReadFile(path)
				if err != nil {
					if !errors.Is(err, fs.ErrNotExist) {
						log.Println("file read: can not read data:", err)
					}
					d.back <- ""
					return
				}
				d.back <- string(b)
			}()
		case <-closer:
			// Wait 1s if writes occur
			// This should avoid mussing writes
			closer = nil
			t = time.NewTimer(1 * time.Second)
			closer2 = t.C
		case <-closer2:
			log.Println("file: finished flush")
			f.flushed <- true
			close(f.flushed)
			return
		}
	}
}
