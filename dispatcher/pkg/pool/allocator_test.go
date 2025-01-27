//     Copyright (C) 2020-2021, IrineSistiana
//
//     This file is part of mosdns.
//
//     mosdns is free software: you can redistribute it and/or modify
//     it under the terms of the GNU General Public License as published by
//     the Free Software Foundation, either version 3 of the License, or
//     (at your option) any later version.
//
//     mosdns is distributed in the hope that it will be useful,
//     but WITHOUT ANY WARRANTY; without even the implied warranty of
//     MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
//     GNU General Public License for more details.
//
//     You should have received a copy of the GNU General Public License
//     along with this program.  If not, see <https://www.gnu.org/licenses/>.

package pool

import (
	"fmt"
	"strconv"
	"testing"
)

func TestAllocator_Get(t *testing.T) {
	alloc := NewAllocator(8) // 256 bytes
	tests := []struct {
		size      int
		wantCap   int
		wantPanic bool
	}{
		{-1, 0, true},   // invalid
		{0, 0, false},   // invalid
		{12, 12, false}, // ignored, too small
		{256, 256, false},
		{257, 257, false}, // ignored, too big
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.size), func(t *testing.T) {
			if tt.wantPanic {
				defer func() {
					msg := recover()
					if msg == nil {
						t.Error("no panic")
					}
				}()
			}

			for i := 0; i < 500; i++ {
				b := alloc.Get(tt.size)
				if len(b) != tt.size {
					t.Fatalf("buffer size, want %d, got %d", tt.size, len(b))
				}
				if cap(b) != tt.wantCap {
					t.Fatalf("buffer cap, want %d, got %d", tt.wantCap, cap(b))
				}
				alloc.Release(b)
			}
		})
	}
}

func Test_shard(t *testing.T) {
	tests := []struct {
		size int
		want int
	}{
		{-1, 0},
		{0, 0},
		{1, 0},
		{2, 1},
		{3, 2},
		{4, 2},
		{5, 3},
		{8, 3},
		{1023, 10},
		{1024, 10},
		{1025, 11},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.size), func(t *testing.T) {
			if got := shard(tt.size); got != tt.want {
				t.Errorf("shard() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAllocator_Put(t *testing.T) {
	alloc := NewAllocator(8) // 256 bytes
	tests := []struct {
		put       []byte
		wantPanic bool
	}{

		{make([]byte, 12), false},  // too small, ignored
		{make([]byte, 254), true},  // invalid
		{make([]byte, 512), false}, // too big, ignored
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(len(tt.put)), func(t *testing.T) {
			if tt.wantPanic {
				defer func() {
					msg := recover()
					if msg == nil {
						t.Error("no panic")
					}
				}()
			}

			alloc.Release(tt.put)
		})
	}
}

func Benchmark_Allocator(b *testing.B) {
	allocator := NewAllocator(16)

	for l := 0; l <= 16; l += 4 {
		bufLen := 1 << l
		b.Run(fmt.Sprintf("length %d", bufLen), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buf := allocator.Get(bufLen)
				allocator.Release(buf)
			}
		})
	}
}

func Benchmark_MakeByteSlice(b *testing.B) {
	for l := 0; l <= 8; l++ {
		bufLen := 1 << l
		b.Run(fmt.Sprintf("length %d", bufLen), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = make([]byte, bufLen)
			}
		})
	}
}
