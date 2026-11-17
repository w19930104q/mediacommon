package remux

// ClockRate is the rate of the unified clock that is used internally when
// remuxing between MPEG-TS and MP4. It matches the MPEG-TS system clock
// (90 kHz): MPEG-TS timestamps are used as-is, while MP4 timestamps are
// rescaled at the container boundary.
const ClockRate = 90000

// rescale converts a timestamp between time scales, rounding half away from zero.
func rescale(v int64, num int64, den int64) int64 {
	if v < 0 {
		return -rescale(-v, num, den)
	}
	return (v*num + den/2) / den
}

// Clock converts the timestamps of a single track between the track time
// scale and the unified clock.
//
// The first DTS that crosses the clock is mapped to zero. This normalizes
// programs whose time base does not start at zero and realigns tracks after
// a program switch, so that the first frame of every segment has offset zero.
//
// DTS and PTS offsets are converted separately and summed afterwards:
// B/P frame reorder offsets are preserved exactly and are not affected by
// the rounding of absolute timestamps.
//
// A Clock converts in a single direction: use one Clock per track and
// per direction.
type Clock struct {
	timeScale uint32
	anchor    int64
	started   bool
}

// NewClock allocates a Clock.
// timeScale is the time scale of the track on the MP4 side; pass ClockRate
// for tracks that already use the unified clock.
func NewClock(timeScale uint32) *Clock {
	return &Clock{
		timeScale: timeScale,
	}
}

// ToUnified converts a (DTS, PTS offset) pair from the track time scale
// into the unified clock.
func (c *Clock) ToUnified(dts int64, ptsOffset int64) (int64, int64) {
	udts := rescale(dts, ClockRate, int64(c.timeScale))

	if !c.started {
		c.started = true
		c.anchor = udts
	}

	udts -= c.anchor
	return udts, udts + rescale(ptsOffset, ClockRate, int64(c.timeScale))
}

// FromUnified converts a (DTS, PTS offset) pair from the unified clock
// into the track time scale.
func (c *Clock) FromUnified(dts int64, ptsOffset int64) (int64, int64) {
	if !c.started {
		c.started = true
		c.anchor = dts
	}

	tdts := rescale(dts-c.anchor, int64(c.timeScale), ClockRate)
	return tdts, tdts + rescale(ptsOffset, int64(c.timeScale), ClockRate)
}
