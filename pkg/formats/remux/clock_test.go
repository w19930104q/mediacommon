package remux_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/remux"
)

func TestClockAnchor(t *testing.T) {
	c := remux.NewClock(remux.ClockRate)

	// the first DTS is mapped to zero, whatever the program time base is
	dts, pts := c.ToUnified(30*90000, 0)
	require.Equal(t, int64(0), dts)
	require.Equal(t, int64(0), pts)

	dts, pts = c.ToUnified(30*90000+3000, 6000)
	require.Equal(t, int64(3000), dts)
	require.Equal(t, int64(9000), pts)
}

func TestClockTimeScaleConversion(t *testing.T) {
	c := remux.NewClock(48000)
	c.ToUnified(0, 0) // anchor

	// 1024 samples at 48kHz = 1920 ticks of the unified clock
	dts, pts := c.ToUnified(1024, 0)
	require.Equal(t, int64(1920), dts)
	require.Equal(t, int64(1920), pts)

	// and back
	c2 := remux.NewClock(48000)
	c2.FromUnified(0, 0) // anchor
	dts, pts = c2.FromUnified(1920, 0)
	require.Equal(t, int64(1024), dts)
	require.Equal(t, int64(1024), pts)
}

func TestClockMonotonic(t *testing.T) {
	// 44100 Hz does not divide the unified clock: converted timestamps
	// must still be strictly increasing
	c := remux.NewClock(44100)

	prev := int64(-1)
	for i := range int64(1000) {
		dts, _ := c.ToUnified(i*1024, 0)
		require.Greater(t, dts, prev)
		prev = dts
	}
}

func TestClockNegativePTSOffset(t *testing.T) {
	c := remux.NewClock(remux.ClockRate)

	c.ToUnified(9000, 0)

	dts, pts := c.ToUnified(12000, -3000)
	require.Equal(t, int64(3000), dts)
	require.Equal(t, int64(0), pts)
}

func TestClockRoundTrip(t *testing.T) {
	// a foreign MP4 time scale: values must survive a round trip
	to := remux.NewClock(12800)
	from := remux.NewClock(12800)

	prev := int64(-1)
	for i := range int64(100) {
		udts, upts := to.ToUnified(i*512, 256)
		require.Greater(t, udts, prev)
		prev = udts

		dts, pts := from.FromUnified(udts, upts-udts)
		require.Equal(t, i*512, dts)
		require.Equal(t, i*512+256, pts)
	}
}
