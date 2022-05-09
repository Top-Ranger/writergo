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

import "github.com/Top-Ranger/writergo/registry"

func init() {
	err := registry.RegisterDataSafe(&Nil{}, "")
	if err != nil {
		panic(err)
	}
	err = registry.RegisterDataSafe(&Nil{}, "Nil")
	if err != nil {
		panic(err)
	}
	err = registry.RegisterDataSafe(&Nil{}, "nil")
	if err != nil {
		panic(err)
	}
}

type Nil struct{}

func (*Nil) SaveWriter(key, data string) error     { return nil }
func (*Nil) LoadWriter(key string) (string, error) { return "", nil }
func (*Nil) LoadConfig(data []byte) error          { return nil }
func (*Nil) IsPermanent() bool                     { return false }
func (*Nil) FlushAndClose()                        {}
