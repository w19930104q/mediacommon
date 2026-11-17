package remux

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
)

// hashParts returns a stable hash of a sequence of byte slices.
func hashParts(parts ...[]byte) [sha256.Size]byte {
	h := sha256.New()
	var sz [4]byte
	for _, p := range parts {
		binary.BigEndian.PutUint32(sz[:], uint32(len(p)))
		h.Write(sz[:])
		h.Write(p)
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// H264Config is a record of the H264 configuration (SPS and PPS) of a track
// being remuxed.
//
// In MPEG-TS, parameter sets travel in-band and can change at any access
// unit. In MP4, they are stored in the initialization block and can only
// change at fragment boundaries. The unified update boundary is the
// random-access access unit: parameter sets carried by random-access access
// units update the record, while parameter sets found anywhere else are
// ignored, since they cannot take effect at a segment boundary.
type H264Config struct {
	// SPS is the recorded sequence parameter set.
	SPS []byte

	// PPS is the recorded picture parameter set.
	PPS []byte

	set bool
}

// Seed records a configuration received out of band, for example from a
// MP4 initialization block.
func (c *H264Config) Seed(sps []byte, pps []byte) {
	c.SPS = sps
	c.PPS = pps
	c.set = true
}

// Update inspects an access unit and, if it is a random-access point that
// carries parameter sets different from the recorded ones, updates the
// record. It returns true when the record has changed: in that case, callers
// must start a new init block / segment before writing the access unit.
func (c *H264Config) Update(au [][]byte) (bool, error) {
	for _, nalu := range au {
		if len(nalu) == 0 {
			return false, fmt.Errorf("empty NALU")
		}
	}

	if !h264.IsRandomAccess(au) {
		return false, nil
	}

	var sps, pps []byte
	for _, nalu := range au {
		switch h264.NALUType(nalu[0] & 0x1F) {
		case h264.NALUTypeSPS:
			sps = nalu

		case h264.NALUTypePPS:
			pps = nalu
		}
	}

	changed := false

	if sps != nil && !bytes.Equal(sps, c.SPS) {
		c.SPS = sps
		changed = true
	}

	if pps != nil && !bytes.Equal(pps, c.PPS) {
		c.PPS = pps
		changed = true
	}

	if changed {
		c.set = true
	}

	return changed, nil
}

// Hash returns a stable hash of the recorded configuration.
// The second return value is false when no configuration has been recorded yet.
func (c *H264Config) Hash() ([sha256.Size]byte, bool) {
	if !c.set {
		return [sha256.Size]byte{}, false
	}
	return hashParts(c.SPS, c.PPS), true
}

// MPEG4AudioConfig is a record of the MPEG-4 Audio configuration of a track
// being remuxed.
//
// Every audio access unit is a random-access point, therefore the record can
// be updated at any access unit boundary; it changes only when the
// configuration actually differs.
type MPEG4AudioConfig struct {
	// Config is the recorded configuration.
	Config mpeg4audio.AudioSpecificConfig

	set bool
}

// Update records a configuration.
// It returns true when the record has changed: in that case, callers must
// start a new init block / segment.
func (c *MPEG4AudioConfig) Update(conf mpeg4audio.AudioSpecificConfig) bool {
	if c.set && c.Config == conf {
		return false
	}

	c.Config = conf
	c.set = true
	return true
}

// Hash returns a stable hash of the recorded configuration.
// The second return value is false when no configuration has been recorded yet.
func (c *MPEG4AudioConfig) Hash() ([sha256.Size]byte, bool) {
	if !c.set {
		return [sha256.Size]byte{}, false
	}

	enc, err := c.Config.Marshal()
	if err != nil {
		return [sha256.Size]byte{}, false
	}

	return hashParts(enc), true
}
