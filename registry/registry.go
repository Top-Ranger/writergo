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

// Package registry provides a central way to register and use all available saving backends.
// All options should be registered prior to the program starting, normally through init().
package registry

import (
	"sync"
)

// AlreadyRegisteredError represents an error where an option is already registeres
type AlreadyRegisteredError string

// Error returns the error description
func (a AlreadyRegisteredError) Error() string {
	return string(a)
}

// DataSafe represents a backend for save storage of writer status.
// All methods must be save for parallel usage.
type DataSafe interface {
	SaveWriter(key, data string) error
	LoadWriter(key string) (string, error)
	LoadConfig(data []byte) error
	IsPermanent() bool
	FlushAndClose()
}

var (
	knownDataSafes      = make(map[string]DataSafe)
	knownDataSafesMutex = sync.RWMutex{}
)

// RegisterDataSafe registeres a data safe.
// The name of the data safe is used as an identifier and must be unique.
// You can savely use it in parallel.
func RegisterDataSafe(t DataSafe, name string) error {
	knownDataSafesMutex.Lock()
	defer knownDataSafesMutex.Unlock()

	_, ok := knownDataSafes[name]
	if ok {
		return AlreadyRegisteredError("DataSafe already registered")
	}
	knownDataSafes[name] = t
	return nil
}

// GetDataSafe returns a data safe.
// The bool indicates whether it existed. You can only use it if the bool is true.
func GetDataSafe(name string) (DataSafe, bool) {
	knownDataSafesMutex.RLock()
	defer knownDataSafesMutex.RUnlock()
	f, ok := knownDataSafes[name]
	return f, ok
}
