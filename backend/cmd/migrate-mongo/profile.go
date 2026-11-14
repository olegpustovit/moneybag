package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// The profiler is mutable server telemetry, not application state; preserve it explicitly as raw BSON.
func (m migration) archiveProfile(ctx context.Context) error {
	exists, err := m.hasProfile(ctx)
	if err != nil || !exists {
		return err
	}
	if m.profileArchive == "" {
		return fmt.Errorf("system.profile requires --profile-archive; it must not be silently skipped")
	}
	if _, err = os.Lstat(m.profileArchive); err == nil {
		return fmt.Errorf("profile archive already exists; choose a new private path")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(m.profileArchive), 0700); err != nil {
		return err
	}
	temp := m.profileArchive + ".tmp"
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	closed := false
	defer func() {
		if !closed {
			file.Close()
		}
	}()
	hash := sha256.New()
	writer := io.MultiWriter(file, hash)
	count, err := m.source.Collection("system.profile").CountDocuments(ctx, bson.M{})
	if err != nil {
		return err
	}
	var copied int64
	if count > 0 {
		cursor, err := m.source.Collection("system.profile").Find(ctx, bson.M{}, options.Find().SetLimit(count).SetBatchSize(1000))
		if err != nil {
			return err
		}
		defer cursor.Close(ctx)
		for cursor.Next(ctx) {
			if _, err = writer.Write(cursor.Current); err != nil {
				return err
			}
			copied++
		}
		if err = cursor.Err(); err != nil {
			return err
		}
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	closed = true
	if err = os.Rename(temp, m.profileArchive); err != nil {
		return err
	}
	fmt.Fprintf(m.out, "archived mutable system.profile %d documents sha256:%x\n", copied, hash.Sum(nil))
	return nil
}
func (m migration) hasProfile(ctx context.Context) (bool, error) {
	names, err := m.source.ListCollectionNames(ctx, bson.M{"name": "system.profile"})
	return len(names) > 0, err
}
func (m migration) verifyProfile(ctx context.Context) error {
	exists, err := m.hasProfile(ctx)
	if err != nil || !exists {
		return err
	}
	file, err := os.Open(m.profileArchive)
	if err != nil {
		return fmt.Errorf("open required profile archive: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	reader := io.TeeReader(file, hash)
	count := 0
	for {
		header := make([]byte, 4)
		if _, err = io.ReadFull(reader, header); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		length := binary.LittleEndian.Uint32(header)
		if length < 5 || length > 16<<20 {
			return fmt.Errorf("invalid profile archive document length")
		}
		raw := make([]byte, int(length))
		copy(raw, header)
		if _, err = io.ReadFull(reader, raw[4:]); err != nil {
			return err
		}
		if err = bson.Raw(raw).Validate(); err != nil {
			return err
		}
		count++
	}
	fmt.Fprintf(m.out, "preserved system.profile operational archive %d documents sha256:%x (live profiler contents are not application state)\n", count, hash.Sum(nil))
	return nil
}
