// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package repstream

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/kv/kvpb"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/util/hlc"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

// decodeTimestampKeyWithKey is a test helper that decodes both timestamp and key.
func decodeTimestampKeyWithKey(encoded []byte) (hlc.Timestamp, roachpb.Key, error) {
	if len(encoded) < 12 {
		return hlc.Timestamp{}, nil, errors.Newf("key too short: got %d bytes", len(encoded))
	}
	ts := hlc.Timestamp{
		WallTime: int64(binary.BigEndian.Uint64(encoded[0:8])),
		Logical:  int32(binary.BigEndian.Uint32(encoded[8:12])),
	}
	if len(encoded) == 12 {
		return ts, nil, nil
	}
	return ts, encoded[12:], nil
}

func TestEncodeDecodeTimestampKey(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ts := func(wall int64) hlc.Timestamp { return hlc.Timestamp{WallTime: wall} }

	pairs := []struct {
		ts  hlc.Timestamp
		key roachpb.Key
	}{
		{ts(1), roachpb.Key("key")},
		{hlc.Timestamp{WallTime: 123, Logical: 456}, roachpb.Key("x")},
		{ts(0), roachpb.Key("a")},
		{ts(0), roachpb.Key("b")},
	}

	sortedPairs := make([][]byte, len(pairs))

	for i, tc := range pairs {
		enc := encodeTimestampKey(tc.ts, tc.key)
		decTs, decKey, err := decodeTimestampKeyWithKey(enc)
		require.NoError(t, err)
		require.True(t, tc.ts.Equal(decTs))
		require.True(t, tc.key.Equal(decKey))
		sortedPairs[i] = enc
	}

	// Test that sorting encoded keys gives us (ts asc, key asc) order.
	slices.SortFunc(sortedPairs, func(a, b []byte) int { return bytes.Compare(a, b) })
	slices.SortFunc(pairs, func(a, b struct {
		ts  hlc.Timestamp
		key roachpb.Key
	}) int {
		cmpTs := a.ts.Compare(b.ts)
		if cmpTs != 0 {
			return cmpTs
		}
		return a.key.Compare(b.key)
	})
	for i, enc := range sortedPairs {
		decTs, decKey, err := decodeTimestampKeyWithKey(enc)
		require.NoError(t, err)
		require.True(t, pairs[i].ts.Equal(decTs))
		require.True(t, pairs[i].key.Equal(decKey))
	}
}

func TestDecodeTimestampKeyTooShort(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	_, err := decodeTimestampKey([]byte("short"))
	require.Error(t, err)
}

func TestDecodeTimestampKeyEmptySuffix(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ts := func(wall int64) hlc.Timestamp { return hlc.Timestamp{WallTime: wall} }
	enc := encodeTimestampKey(ts(1), nil)
	require.Len(t, enc, 12)
	_, err := decodeTimestampKey(enc)
	require.Error(t, err)
}

func TestDeepCopyRangeFeedValueDoesNotAliasCaller(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	kv := &kvpb.RangeFeedValue{
		Key: roachpb.Key("original-key"),
		Value: roachpb.Value{
			RawBytes:  []byte("v1"),
			Timestamp: hlc.Timestamp{WallTime: 1},
		},
		PrevValue: roachpb.Value{RawBytes: []byte("prev1")},
	}
	copied := deepCopyRangeFeedValue(kv)

	require.NotEmpty(t, kv.Key)
	kv.Key[0] = 'Z'
	require.Equal(t, roachpb.Key("original-key"), copied.Key)

	require.NotEmpty(t, kv.Value.RawBytes)
	kv.Value.RawBytes[0] = 0xff
	require.Equal(t, []byte("v1"), copied.Value.RawBytes)

	require.NotEmpty(t, kv.PrevValue.RawBytes)
	kv.PrevValue.RawBytes[0] = 0xff
	require.Equal(t, []byte("prev1"), copied.PrevValue.RawBytes)
}

func TestDeepCopyRangeFeedDeleteRangeDoesNotAliasCaller(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	d := &kvpb.RangeFeedDeleteRange{
		Span:      roachpb.Span{Key: roachpb.Key("a"), EndKey: roachpb.Key("b")},
		Timestamp: hlc.Timestamp{WallTime: 1},
	}
	copied := deepCopyRangeFeedDeleteRange(d)

	d.Span.Key[0] = 'z'
	require.Equal(t, roachpb.Key("a"), copied.Span.Key)

	d.Span.EndKey[0] = 'z'
	require.Equal(t, roachpb.Key("b"), copied.Span.EndKey)
}
