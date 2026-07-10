/*
Copyright The Helm Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package strvals

import (
	"testing"
)

// Every parser entry point must turn malformed user input into an error, never a
// panic: --set/--set-string/--set-file/--set-literal values come straight from the
// command line. The parsers recover from the type-assertion panics that malformed
// keys provoke (e.g. `a=1,a[0]=2`), so these targets assert that containment holds
// for each distinct state machine and reader, not just for Parse.

func FuzzParse(f *testing.F) {
	f.Fuzz(func(_ *testing.T, data string) {
		_, _ = Parse(data)
	})
}

// ParseString forces every value to a string, taking a different branch of the
// value decoder than Parse.
func FuzzParseString(f *testing.F) {
	f.Add("a=1,b=2")
	f.Add("a[0].b=1")
	f.Fuzz(func(_ *testing.T, data string) {
		_, _ = ParseString(data)
	})
}

// ParseIntoFile drives the reader callback (app2kube's --set-file path). The
// reader is trivial here so the fuzzer exercises the key grammar and the
// list/map construction around it, not file IO.
func FuzzParseIntoFile(f *testing.F) {
	f.Add("a=path")
	f.Add("a[0].b=path,c=other")
	reader := func(rs []rune) (any, error) { return string(rs), nil }
	f.Fuzz(func(_ *testing.T, data string) {
		dest := map[string]any{}
		_ = ParseIntoFile(data, dest, reader)
	})
}

// ParseJSON has its own value path: a json.Decoder plus byte-offset arithmetic
// (InputOffset/io.CopyN) that Parse never touches.
func FuzzParseJSON(f *testing.F) {
	f.Add(`a={"b":1}`)
	f.Add(`a[0]=[1,2],b={"c":{"d":null}}`)
	f.Add(`a={`)
	f.Fuzz(func(_ *testing.T, data string) {
		dest := map[string]any{}
		_ = ParseJSON(data, dest)
	})
}

// ParseLiteral is a separate state machine (literalParser) with its own key
// scanner and an empty stop-set value reader.
func FuzzParseLiteral(f *testing.F) {
	f.Add("a=b")
	f.Add(`a[0].b=raw,value`)
	f.Fuzz(func(_ *testing.T, data string) {
		_, _ = ParseLiteral(data)
	})
}

// ParseLiteralInto writes into a caller-supplied map, exercising the merge path
// that ParseLiteral's fresh map hides (e.g. overwriting a scalar with a subtree).
func FuzzParseLiteralInto(f *testing.F) {
	f.Add("a=b")
	f.Fuzz(func(_ *testing.T, data string) {
		dest := map[string]any{"a": "seed", "list": []any{"x"}}
		_ = ParseLiteralInto(data, dest)
	})
}
