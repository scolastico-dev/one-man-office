// Package boundedzip bounds ZIP central-directory allocations before parsing.
package boundedzip

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"errors"
	"io"
)

// Central directory data is retained in memory by archive/zip.NewReader. Bound
// it, its actual entry count and cancellation before calling that constructor.
const MaxDirectoryBytes = 32 << 20

var ErrLimit = errors.New("zip archive is invalid or exceeds entry/metadata limits")

type Limits struct {
	ArchiveBytes int64
	Entries      int
}

// NewReader preflights actual central-directory records before the standard
// library allocates entries. File payload/extraction rules remain the caller's.
func NewReader(ctx context.Context, r io.ReaderAt, size int64, limits Limits) (*zip.Reader, error) {
	if err := Check(ctx, r, size, limits); err != nil {
		return nil, err
	}
	return zip.NewReader(r, size)
}

func Check(ctx context.Context, r io.ReaderAt, size int64, limits Limits) error {
	if limits.ArchiveBytes <= 0 || limits.Entries <= 0 {
		return ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if size < 22 || size > limits.ArchiveBytes {
		return ErrLimit
	}
	tailSize := min(size, int64(22+65535))
	tail := make([]byte, tailSize)
	if _, err := r.ReadAt(tail, size-tailSize); err != nil {
		return ErrLimit
	}
	end := -1
	for i := len(tail) - 22; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) == 0x06054b50 {
			// Use the same last signature as archive/zip. Never skip a
			// later malformed signature and validate a different directory.
			if i+22+int(binary.LittleEndian.Uint16(tail[i+20:])) != len(tail) {
				return ErrLimit
			}
			end = i
			break
		}
	}
	if end < 0 {
		return ErrLimit
	}
	e := tail[end:]
	endOffset := size - tailSize + int64(end)
	if binary.LittleEndian.Uint16(e[4:]) != 0 || binary.LittleEndian.Uint16(e[6:]) != 0 {
		return ErrLimit
	}
	count := uint64(binary.LittleEndian.Uint16(e[10:]))
	directorySize := uint64(binary.LittleEndian.Uint32(e[12:]))
	directoryOffset := uint64(binary.LittleEndian.Uint32(e[16:]))
	directoryEnd := uint64(endOffset)
	// Some Go versions probe ZIP64 for a 0xffff size, others 0xffffffff.
	// Require a count/offset sentinel for size-only ZIP64 to avoid validating
	// one directory while the standard reader selects a different one.
	if directorySize == 0xffffffff && count != 0xffff && directoryOffset != 0xffffffff {
		return ErrLimit
	}
	// Go also probes a ZIP64 locator when a 32-bit directory size equals
	// 0xffff; match that behavior so both parsers select the same metadata.
	hasLocator := false
	if directorySize == 0xffff && endOffset >= 20 {
		var signature [4]byte
		if _, err := r.ReadAt(signature[:], endOffset-20); err != nil {
			return ErrLimit
		}
		hasLocator = binary.LittleEndian.Uint32(signature[:]) == 0x07064b50
	}
	if count == 0xffff || directorySize == 0xffffffff || directoryOffset == 0xffffffff || hasLocator {
		// ZIP64 has a bounded fixed header plus optional extensible data. Its
		// locator must refer to this same single disk and immediately precede EOCD.
		var locator [20]byte
		if endOffset < 20 {
			return ErrLimit
		}
		if _, err := r.ReadAt(locator[:], endOffset-20); err != nil {
			return ErrLimit
		}
		if binary.LittleEndian.Uint32(locator[:]) != 0x07064b50 || binary.LittleEndian.Uint32(locator[4:]) != 0 || binary.LittleEndian.Uint32(locator[16:]) != 1 {
			return ErrLimit
		}
		off := binary.LittleEndian.Uint64(locator[8:])
		if off > uint64(endOffset-20) || uint64(endOffset-20)-off < 56 {
			return ErrLimit
		}
		var record [56]byte
		if _, err := r.ReadAt(record[:], int64(off)); err != nil {
			return ErrLimit
		}
		recordSize := binary.LittleEndian.Uint64(record[4:])
		if binary.LittleEndian.Uint32(record[:]) != 0x06064b50 || recordSize < 44 || recordSize > 4096 || off+12+recordSize != uint64(endOffset-20) || binary.LittleEndian.Uint32(record[16:]) != 0 || binary.LittleEndian.Uint32(record[20:]) != 0 {
			return ErrLimit
		}
		count = binary.LittleEndian.Uint64(record[32:])
		if binary.LittleEndian.Uint64(record[24:]) != count {
			return ErrLimit
		}
		directorySize = binary.LittleEndian.Uint64(record[40:])
		directoryOffset = binary.LittleEndian.Uint64(record[48:])
		directoryEnd = off
	} else if binary.LittleEndian.Uint16(e[8:]) != uint16(count) {
		return ErrLimit
	}
	if count > uint64(limits.Entries) || directorySize > MaxDirectoryBytes || directoryOffset > directoryEnd || directorySize != directoryEnd-directoryOffset {
		return ErrLimit
	}
	var header [46]byte
	actual := uint64(0)
	for offset := directoryOffset; offset < directoryEnd; {
		if err := ctx.Err(); err != nil {
			return err
		}
		actual++
		if actual > uint64(limits.Entries) || directoryEnd-offset < 46 {
			return ErrLimit
		}
		if _, err := r.ReadAt(header[:], int64(offset)); err != nil {
			return ErrLimit
		}
		if binary.LittleEndian.Uint32(header[:]) != 0x02014b50 {
			return ErrLimit
		}
		nameSize := uint64(binary.LittleEndian.Uint16(header[28:]))
		extraSize := uint64(binary.LittleEndian.Uint16(header[30:]))
		commentSize := uint64(binary.LittleEndian.Uint16(header[32:]))
		length := 46 + nameSize + extraSize + commentSize
		if nameSize == 0 || nameSize > 4096 || length > directoryEnd-offset || binary.LittleEndian.Uint16(header[34:]) != 0 {
			return ErrLimit
		}
		offset += length
	}
	if actual != count {
		return ErrLimit
	}
	return nil
}
