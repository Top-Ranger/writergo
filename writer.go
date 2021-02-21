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
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	commandInitialAsk  = "get_initial"
	commandInitialGet  = "current_state"
	commandInitialSend = "state"
	commandNumberUser  = "number_user"
	commandAskWrite    = "write"
	commandGetWrite    = "can_write"
	commandStopWrite   = "can_not_write"
)

type writer struct {
	Key string

	l           sync.Mutex
	connections map[string]*websocket.Conn
	counter     int
	initial     struct {
		l sync.Mutex
		c chan string
	}

	active string

	changeActiveLock sync.Mutex
}

type command struct {
	Comm string
	Data string
}

func (w *writer) AddNew(conn *websocket.Conn) error {
	w.l.Lock()
	defer w.l.Unlock()

	if w.connections == nil {
		w.connections = make(map[string]*websocket.Conn)
	}

	// Create new channel to avoid old data
	w.initial.l.Lock()
	w.initial.c = make(chan string, len(w.connections))
	w.initial.l.Unlock()

	key := strconv.Itoa(w.counter)
	w.counter++

	c := command{Comm: commandInitialAsk}
	// Get initial state
	initialState := ""
initialLoop:
	for k := range w.connections {
		err := w.connections[k].WriteJSON(&c)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Println(w.Key, k, "ask initial state:", err)
			}
			w.Remove(k)
			continue initialLoop
		}

		t := time.NewTimer(time.Duration(config.SyncSeconds) * time.Second)
		select {
		case initialState = <-w.initial.c:
			if !t.Stop() {
				<-t.C
			}
			break initialLoop
		case <-t.C:
			log.Println(w.Key, k, "initial timeout:", err)
			w.Remove(k)
			continue initialLoop
		}
	}

	c = command{Comm: commandInitialSend, Data: initialState}
	err := conn.WriteJSON(&c)
	if err != nil {
		if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
			log.Println(w.Key, key, "send initial state:", err)
		}
		return err
	}

	go writerWorker(conn, key, w)

	w.connections[key] = conn

	log.Println(w.Key, "added:", key)

	w.push(command{Comm: commandNumberUser, Data: strconv.Itoa(len(w.connections))}, "")

	return nil
}

func (w *writer) Remove(key string) {
	go func() {
		w.l.Lock()
		defer w.l.Unlock()

		conn := w.connections[key]
		if conn != nil {
			if err := conn.Close(); err != nil {
				log.Println(w.Key, "close:", err)
			}
		}
		delete(w.connections, key)

		log.Println(w.Key, "removed:", key)

		w.push(command{Comm: commandNumberUser, Data: strconv.Itoa(len(w.connections))}, "")
	}()
}

func (w *writer) CanBeDeleted() bool {
	w.l.Lock()
	defer w.l.Unlock()

	return len(w.connections) == 0
}

func (w *writer) push(data command, sender string) {
	go func() {
		w.l.Lock()
		defer w.l.Unlock()
		for k := range w.connections {
			if k != sender {
				err := w.connections[k].WriteJSON(&data)
				if err != nil {
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
						log.Println(w.Key, k, "write command:", err)
					}
					w.Remove(k)
				}
			}
		}
	}()
}

func (w *writer) changeActive(key string) {
	go func() {
		w.changeActiveLock.Lock()
		defer w.changeActiveLock.Unlock()

		w.l.Lock()
		conn := w.connections[w.active]
		if conn != nil {
			c := command{Comm: commandStopWrite}
			err := conn.WriteJSON(&c)
			if err != nil {
				w.Remove(key)
			}
		}
		w.l.Unlock()

		time.Sleep(time.Duration(config.SyncSeconds) * time.Second)

		w.l.Lock()
		defer w.l.Unlock()

		w.active = key
		conn = w.connections[key]
		if conn != nil {
			c := command{Comm: commandGetWrite}
			err := conn.WriteJSON(&c)
			if err != nil {
				w.Remove(key)
			}
		}
		log.Println(w.Key, key, "active")
	}()
}

func writerWorker(conn *websocket.Conn, key string, w *writer) {
	for {
		time.Sleep(10 * time.Millisecond)
		var c command
		err := conn.ReadJSON(&c)
		if err != nil {
			// Stop on error - something went wrong
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Println(w.Key, key, "socket error:", err)
			}
			w.Remove(key)
			return
		}
		switch c.Comm {
		case commandInitialGet:
			w.initial.l.Lock()
			select {
			case w.initial.c <- c.Data:
				// Do nothing
			default:
				log.Println(w.Key, key, "get initial:", "channel blocked")
			}
			w.initial.l.Unlock()
		case commandInitialSend:
			w.l.Lock()
			currentActive := w.active
			w.l.Unlock()
			if currentActive != key {
				w.Remove(key)
				return
			}
			w.push(c, key)
		case commandAskWrite:
			w.changeActive(key)
		default:
			log.Println(w.Key, key, "unknown control:", c.Comm)
		}
	}
}
