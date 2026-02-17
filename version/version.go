/*
   Copyright Mycophonic.

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

package version

//nolint:gochecknoglobals // Set via ldflags at build time.
var (
	version = "0.1.0-dev"
	name    = "saprobe"
	commit  = "undefined"
	date    = "undefined"
)

// Commit returns the compile time commit.
func Commit() string {
	return commit
}

// Version returns the compile time version.
func Version() string {
	return version
}

// Name returns the compile time name.
func Name() string {
	return name
}

// Date returns the compile time build date.
func Date() string {
	return date
}
