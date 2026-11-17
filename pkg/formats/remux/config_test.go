package remux_test

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/remux"
)

var (
	testSPSA = []byte{7, 1, 2, 3}
	testPPSA = []byte{8, 1}
	testSPSB = []byte{7, 4, 5, 6}
	testPPSB = []byte{8, 2}
)

func TestH264ConfigUpdateBoundary(t *testing.T) {
	c := &remux.H264Config{}

	_, ok := c.Hash()
	require.False(t, ok)

	// parameter sets carried by a non-random-access AU are ignored
	changed, err := c.Update([][]byte{testSPSA, testPPSA, {1}})
	require.NoError(t, err)
	require.False(t, changed)

	_, ok = c.Hash()
	require.False(t, ok)

	// parameter sets carried by a random-access AU update the record
	changed, err = c.Update([][]byte{testSPSA, testPPSA, {5}})
	require.NoError(t, err)
	require.True(t, changed)

	h1, ok := c.Hash()
	require.True(t, ok)

	// identical parameter sets: no change
	changed, err = c.Update([][]byte{testSPSA, testPPSA, {5}})
	require.NoError(t, err)
	require.False(t, changed)

	// a program switch: new parameter sets at a random-access boundary
	changed, err = c.Update([][]byte{testSPSB, testPPSB, {5}})
	require.NoError(t, err)
	require.True(t, changed)

	h2, ok := c.Hash()
	require.True(t, ok)
	require.NotEqual(t, h1, h2)

	// new parameter sets outside a random-access boundary are still ignored
	changed, err = c.Update([][]byte{testSPSA, testPPSA, {1}})
	require.NoError(t, err)
	require.False(t, changed)

	h3, ok := c.Hash()
	require.True(t, ok)
	require.Equal(t, h2, h3)
}

func TestH264ConfigSeedAndHash(t *testing.T) {
	c := &remux.H264Config{}
	c.Seed(testSPSA, testPPSA)

	h, ok := c.Hash()
	require.True(t, ok)

	// the hash is the SHA-256 of length-prefixed SPS and PPS
	exp := sha256.New()
	exp.Write([]byte{0, 0, 0, 4})
	exp.Write(testSPSA)
	exp.Write([]byte{0, 0, 0, 2})
	exp.Write(testPPSA)
	require.Equal(t, exp.Sum(nil), h[:])
}

func TestH264ConfigEmptyNALU(t *testing.T) {
	c := &remux.H264Config{}
	_, err := c.Update([][]byte{{5}, {}})
	require.EqualError(t, err, "empty NALU")
}

func TestMPEG4AudioConfig(t *testing.T) {
	c := &remux.MPEG4AudioConfig{}

	_, ok := c.Hash()
	require.False(t, ok)

	changed := c.Update(mpeg4audio.AudioSpecificConfig{
		Type:          mpeg4audio.ObjectTypeAACLC,
		SampleRate:    48000,
		ChannelConfig: 2,
	})
	require.True(t, changed)

	h1, ok := c.Hash()
	require.True(t, ok)

	// identical configuration: no change
	changed = c.Update(mpeg4audio.AudioSpecificConfig{
		Type:          mpeg4audio.ObjectTypeAACLC,
		SampleRate:    48000,
		ChannelConfig: 2,
	})
	require.False(t, changed)

	// different configuration: the record changes
	changed = c.Update(mpeg4audio.AudioSpecificConfig{
		Type:          mpeg4audio.ObjectTypeAACLC,
		SampleRate:    44100,
		ChannelConfig: 2,
	})
	require.True(t, changed)

	h2, ok := c.Hash()
	require.True(t, ok)
	require.NotEqual(t, h1, h2)
}
