// Package remux provides a shared clock and codec configuration records
// for remuxing between MPEG-TS and MP4.
//
// MPEG-TS and MP4 keep timestamps in different time scales and carry codec
// configurations in different places (in-band in MPEG-TS, in the
// initialization block of MP4). When the two are converted independently,
// remuxed streams end up with misaligned first-frame offsets, jittering
// B/P frame reorder offsets and stale configurations after a program
// switch. This package provides the single boundary at which both
// conversions happen, so that segments keep exact durations, DTS/PTS stay
// monotonic and codec configurations change only at well-defined points.
package remux
