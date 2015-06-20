//  Copyright (c) 2014 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.

package errors

import (
)

// Shell errors -- errors in the command line shell

const (
	CANNOT_CONNECT = 100
)

func NewShellErrorCannotConnect(msg string) Error {
	return &err{level: EXCEPTION, ICode: CANNOT_CONNECT, IKey: "shell.cannot_connect", InternalMsg: msg, InternalCaller: CallerN(1)}
}