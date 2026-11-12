// Copyright 2026 ALTESSA SOLUTIONS INC.
// Use of this source code is governed by the MIT license that can be found in
// the LICENSE file.

package fieldinfo_test

import (
	"testing"

	"github.com/altessa-s/go-aip/fieldinfo"

	testpb "github.com/altessa-s/go-aip/internal/proto/gen/fieldinfotest/v1"
	annotations "google.golang.org/genproto/googleapis/api/annotations"
)

func BenchmarkValidate(b *testing.B) {
	host := validHost()

	b.ReportAllocs()

	for b.Loop() {
		if err := fieldinfo.Validate(host); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidate_NoFormats(b *testing.B) {
	plain := &testpb.Plain{Name: "x", Child: &testpb.Plain{Name: "y"}}

	b.ReportAllocs()

	for b.Loop() {
		if err := fieldinfo.Validate(plain); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNormalizeValue(b *testing.B) {
	for _, bc := range []struct {
		name   string
		format annotations.FieldInfo_Format
		value  string
	}{
		{"UUID4", annotations.FieldInfo_UUID4, "F47AC10B-58CC-4372-A567-0E02B2C3D479"},
		{"IPv4", annotations.FieldInfo_IPV4, "001.022.233.040"},
		{"IPv6", annotations.FieldInfo_IPV6, "2001:0DB8:0::0"},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				if _, err := fieldinfo.NormalizeValue(bc.format, bc.value); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFormatOf(b *testing.B) {
	fields := validHost().ProtoReflect().Descriptor().Fields()

	b.ReportAllocs()

	i := 0
	for b.Loop() {
		_ = fieldinfo.FormatOf(fields.Get(i % fields.Len()))
		i++
	}
}

func BenchmarkFormatOf_Parallel(b *testing.B) {
	fields := validHost().ProtoReflect().Descriptor().Fields()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = fieldinfo.FormatOf(fields.Get(i % fields.Len()))
			i++
		}
	})
}

func BenchmarkValidate_Parallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		host := validHost()
		for pb.Next() {
			if err := fieldinfo.Validate(host); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
