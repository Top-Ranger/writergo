//go:build mysql

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
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Top-Ranger/writergo/registry"
	_ "github.com/go-sql-driver/mysql"
)

func init() {
	err := registry.RegisterDataSafe(&MySQL{}, "MySQL")
	if err != nil {
		panic(err)
	}
	err = registry.RegisterDataSafe(&MySQL{}, "mysql")
	if err != nil {
		panic(err)
	}
}

// MySQLMaxLengthID is the maximum supported writer id length
const MySQLMaxLengthID = 500

// ErrMySQLUnknownID is returned when the requested writer id is too long
var ErrMySQLIDtooLong = errors.New("mysql: id is too long")

// ErrMySQLNotConfigured is returned when the database is used before it is configured
var ErrMySQLNotConfigured = errors.New("mysql: usage before configuration is used")

type MySQL struct {
	dsn string
	db  *sql.DB
}

func (m *MySQL) SaveWriter(key, data string) error {
	if m.db == nil {
		return ErrMySQLNotConfigured
	}

	if len(key) > MySQLMaxLengthID {
		return ErrMySQLIDtooLong
	}
	_, err := m.db.Exec("REPLACE writer (`key`, data) VALUES (?,?)", key, data)
	return err
}

func (m *MySQL) LoadWriter(key string) (string, error) {
	if m.db == nil {
		return "", ErrMySQLNotConfigured
	}

	if len(key) > MySQLMaxLengthID {
		return "", ErrMySQLIDtooLong
	}

	rows, err := m.db.Query("SELECT data FROM writer WHERE `key`=?", key)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	if rows.Next() {
		var s string
		err = rows.Scan(&s)
		return s, err
	}

	// No data in DB
	return "", nil
}

func (m *MySQL) LoadConfig(data []byte) error {
	m.dsn = string(data)
	db, err := sql.Open("mysql", m.dsn)
	if err != nil {
		return fmt.Errorf("mysql: can not open '%s': %w", m.dsn, err)
	}
	db.SetConnMaxLifetime(time.Minute * 1)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	m.db = db
	return nil
}

func (m *MySQL) IsPermanent() bool {
	return true
}

func (m *MySQL) FlushAndClose() {
	m.db.Close()
}
