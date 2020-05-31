// Copyright 2020 The Ebiten Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package shader_test

import (
	"testing"

	. "github.com/hajimehoshi/ebiten/internal/shader"
)

func TestDump(t *testing.T) {
	tests := []struct {
		Name string
		Src  string
		VS   string
		FS   string
	}{
		{
			Name: "uniforms",
			Src: `package main

var (
	Foo vec2
	Boo vec4
)`,
			VS: `uniform vec2 U0;
uniform vec4 U1;`,
			FS: `uniform vec2 U0;
uniform vec4 U1;`,
		},
		{
			Name: "func",
			Src: `package main

func Foo(foo vec2) vec4 {
}`,
			VS: `void F0(in vec2 l0, out vec4 l1) {
}`,
			FS: `void F0(in vec2 l0, out vec4 l1) {
}`,
		},
		{
			Name: "func body",
			Src: `package main

func Foo(foo vec2) vec4 {
	return vec4(foo, 0, 1)
}`,
			VS: `void F0(in vec2 l0, out vec4 l1) {
	l1 = vec4(l0, 0, 1);
	return;
}`,
			FS: `void F0(in vec2 l0, out vec4 l1) {
	l1 = vec4(l0, 0, 1);
	return;
}`,
		},
		{
			Name: "func multiple out params",
			Src: `package main

func Foo(foo vec4) (float, float, float, float) {
	return foo.x, foo.y, foo.z, foo.w
}`,
			VS: `void F0(in vec4 l0, out float l1, out float l2, out float l3, out float l4) {
	l1 = (l0).x;
	l2 = (l0).y;
	l3 = (l0).z;
	l4 = (l0).w;
	return;
}`,
			FS: `void F0(in vec4 l0, out float l1, out float l2, out float l3, out float l4) {
	l1 = (l0).x;
	l2 = (l0).y;
	l3 = (l0).z;
	l4 = (l0).w;
	return;
}`,
		},
	}
	for _, tc := range tests {
		s, err := Compile([]byte(tc.Src))
		if err != nil {
			t.Error(err)
			continue
		}
		vs, fs := s.Glsl()
		if got, want := vs, tc.VS+"\n"; got != want {
			t.Errorf("%s: got: %v, want: %v", tc.Name, got, want)
		}
		if got, want := fs, tc.FS+"\n"; got != want {
			t.Errorf("%s: got: %v, want: %v", tc.Name, got, want)
		}
	}
}
