//  Copyright (c) 2018 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.

package execution

import (
	"fmt"
	"testing"

	"github.com/couchbase/query/value"
)

func TestHashTable(t *testing.T) {

	var count, dup int
	var e error
	var intVal, strVal, inputVal1, inputVal2, outputVal value.Value

	// create a hash table
	htab := NewHashTable()

	// insert values into hash table
	for i := 0; i < 4096; i++ {
		if (i & 0xfff) == 0 {
			dup = 25
		} else if (i & 0xff) == 0 {
			dup = 5
		} else {
			dup = 1
		}

		intVal = value.NewValue(i)
		strVal = value.NewValue(fmt.Sprintf("this is string %d", i))
		for j := 0; j < dup; j++ {
			inputVal1 = value.NewValue(fmt.Sprintf("this is payload value for int hash value i = %d j = %d", i, j))
			inputVal2 = value.NewValue(fmt.Sprintf("this is payload value for string hash value i = %d j = %d", i, j))

			e = htab.Put(intVal, inputVal1)
			if e != nil {
				t.Errorf("PUT of int value failed, i = %d j = %d", i, j)
			}

			e = htab.Put(strVal, inputVal2)
			if e != nil {
				t.Errorf("PUT of string value failed, i = %d j = %d", i, j)
			}
		}
	}

	// retrieve values from hash table
	for i := -2; i < 4100; i++ {
		if i < 0 || i >= 4096 {
			dup = 0
		} else if (i & 0xfff) == 0 {
			dup = 25
		} else if (i & 0xff) == 0 {
			dup = 5
		} else {
			dup = 1
		}

		intVal = value.NewValue(i)
		strVal = value.NewValue(fmt.Sprintf("this is string %d", i))

		count = 0
		outputVal, e = htab.Get(intVal)
		if e != nil {
			t.Errorf("GET of int value failed, i = %d j = %d", i, count)
		}
		if outputVal != nil {
			count++
			for {
				outputVal, e = htab.GetNext()
				if e != nil {
					t.Errorf("GET of int value failed, i = %d j = %d", i, count)
				}

				if outputVal == nil {
					break
				}
				count++
			}
		}
		if count != dup {
			t.Errorf("Unexpected number of results for int value, expect %d get %d", dup, count)
		}

		count = 0
		outputVal, e = htab.Get(strVal)
		if e != nil {
			t.Errorf("GET of string value failed, i = %d j = %d", i, count)
		}
		if outputVal != nil {
			count++
			for {
				outputVal, e = htab.GetNext()
				if e != nil {
					t.Errorf("GET of string value failed, i = %d j = %d", i, count)
				}

				if outputVal == nil {
					break
				}
				count++
			}
		}
		if count != dup {
			t.Errorf("Unexpected number of results for string value, expect %d get %d", dup, count)
		}
	}

	// iterate through hash table
	count = 0
	for {
		outputVal = htab.Iterate()
		if outputVal == nil {
			break
		}

		count++
	}
	// should have 4180 intVal and 4180 strVal
	if count != 8360 {
		t.Errorf("Incorrect number of entries from Iterate(), expect 8360, get %d", count)
	}

	if htab.NumBuckets() != 16384 {
		t.Errorf("Incorrect number of buckets in hash table, expect 16384, get %d", htab.NumBuckets())
	}

	// drop the hash table
	htab.Drop()
}
