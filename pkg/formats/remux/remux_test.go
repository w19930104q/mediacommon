package remux_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"sort"
	"testing"

	"github.com/asticode/go-astits"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	mp4codecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	tscodecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/remux"
)

// fixed short test stream: 2 video GOPs of 1 second each (30 fps, IBBP
// structure with B/P reordering) plus MPEG-4 Audio at 48 kHz, on a program
// time base that does not start at zero.
const (
	videoStart     = 30 * 90000 // non-zero program time base
	frameDuration  = 3000       // 30 fps in 90 kHz
	gopSize        = 30         // frames per GOP = 1 second per segment
	gopCount       = 2
	videoFrameNum  = gopSize * gopCount
	ptsDelay       = 2 * frameDuration // B-frame reorder delay
	audioStart     = videoStart + ptsDelay
	audioFrameDur  = 1920 // 1024 samples at 48 kHz, in 90 kHz
	audioFrameNum  = 93
	videoTimeScale = 90000
	audioTimeScale = 48000
)

// presentation indices of the frames of a GOP, in decode order:
// I0 P3 B1 B2 P6 B4 B5 ... P27 B25 B26 B28 B29
var gopOrder = []int{
	0, 3, 1, 2, 6, 4, 5, 9, 7, 8, 12, 10, 11, 15, 13, 14,
	18, 16, 17, 21, 19, 20, 24, 22, 23, 27, 25, 26, 28, 29,
}

var testRemuxH264SPS = []byte{
	0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02,
	0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04,
	0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9,
	0x20,
}

var testRemuxH264PPS = []byte{8}

var testAudioConfig = mpeg4audio.AudioSpecificConfig{
	Type:          mpeg4audio.ObjectTypeAACLC,
	SampleRate:    48000,
	ChannelConfig: 2,
}

func videoAU(presIdxInGOP int) [][]byte {
	if presIdxInGOP == 0 {
		return [][]byte{
			testRemuxH264SPS, // SPS
			testRemuxH264PPS, // PPS
			{5},              // IDR
		}
	}
	return [][]byte{{1}} // non-IDR
}

// presentation index of the n-th frame in decode order.
func presIndex(n int) int {
	return (n/gopSize)*gopSize + gopOrder[n%gopSize]
}

type videoSample struct {
	pts int64
	dts int64
	au  [][]byte
}

type audioSample struct {
	pts int64
	au  []byte
}

func normAudioConf(c mpeg4audio.AudioSpecificConfig) mpeg4audio.AudioSpecificConfig {
	return mpeg4audio.AudioSpecificConfig{
		Type:          c.Type,
		SampleRate:    c.SampleRate,
		ChannelConfig: c.ChannelConfig,
	}
}

func writeSourceTS(t *testing.T) []byte {
	t.Helper()

	vidTrack := &mpegts.Track{Codec: &tscodecs.H264{}}
	audTrack := &mpegts.Track{Codec: &tscodecs.MPEG4Audio{Config: testAudioConfig}}

	var buf bytes.Buffer
	w := &mpegts.Writer{W: &buf, Tracks: []*mpegts.Track{vidTrack, audTrack}}
	require.NoError(t, w.Initialize())

	type event struct {
		sortKey int64
		isVideo bool
		idx     int
	}

	events := make([]event, 0, videoFrameNum+audioFrameNum)
	for n := range videoFrameNum {
		events = append(events, event{videoStart + int64(n)*frameDuration, true, n})
	}
	for i := range audioFrameNum {
		events = append(events, event{audioStart + int64(i)*audioFrameDur, false, i})
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].sortKey < events[j].sortKey })

	for _, ev := range events {
		if ev.isVideo {
			n := ev.idx
			dts := videoStart + int64(n)*frameDuration
			pts := videoStart + ptsDelay + int64(presIndex(n))*frameDuration
			require.NoError(t, w.WriteH264(vidTrack, pts, dts, videoAU(presIndex(n)%gopSize)))
		} else {
			i := ev.idx
			pts := audioStart + int64(i)*audioFrameDur
			require.NoError(t, w.WriteMPEG4Audio(audTrack, pts, [][]byte{{0x11, byte(i), 0x22}}))
		}
	}

	return buf.Bytes()
}

func readTS(t *testing.T, byts []byte) ([]videoSample, []audioSample, *mpegts.Track, *mpegts.Track) {
	t.Helper()

	r := &mpegts.Reader{R: bytes.NewReader(byts)}
	require.NoError(t, r.Initialize())

	var vidTrack, audTrack *mpegts.Track
	for _, track := range r.Tracks() {
		switch track.Codec.(type) {
		case *tscodecs.H264:
			vidTrack = track
		case *tscodecs.MPEG4Audio:
			audTrack = track
		}
	}
	require.NotNil(t, vidTrack)
	require.NotNil(t, audTrack)

	var vid []videoSample
	var aud []audioSample

	r.OnDataH264(vidTrack, func(pts int64, dts int64, au [][]byte) error {
		vid = append(vid, videoSample{pts: pts, dts: dts, au: au})
		return nil
	})

	r.OnDataMPEG4Audio(audTrack, func(pts int64, aus [][]byte) error {
		require.Len(t, aus, 1)
		aud = append(aud, audioSample{pts: pts, au: aus[0]})
		return nil
	})

	for {
		err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}

	return vid, aud, vidTrack, audTrack
}

// requirePCRInRange checks that all PCRs of a MPEG-TS stream are valid and
// close to the stream time base (a re-anchored stream must not produce
// negative PCRs that wrap around the 33-bit PCR field).
func requirePCRInRange(t *testing.T, byts []byte, maxBase int64) {
	t.Helper()

	dem := astits.NewDemuxer(context.Background(), bytes.NewReader(byts), astits.DemuxerOptPacketSize(188))

	found := false
	for {
		data, err := dem.NextData()
		if errors.Is(err, astits.ErrNoMorePackets) {
			break
		}
		require.NoError(t, err)

		if data.FirstPacket != nil &&
			data.FirstPacket.AdaptationField != nil &&
			data.FirstPacket.AdaptationField.PCR != nil {
			base := data.FirstPacket.AdaptationField.PCR.Base
			require.GreaterOrEqual(t, base, int64(0))
			require.LessOrEqual(t, base, maxBase)
			found = true
		}
	}
	require.True(t, found)
}

func TestRemuxBidirectional(t *testing.T) {
	tsData := writeSourceTS(t)

	//
	// MPEG-TS -> fMP4
	//

	vid, aud, _, audTrack := readTS(t, tsData)
	require.Len(t, vid, videoFrameNum)
	require.Len(t, aud, audioFrameNum)

	// the audio configuration found in the MPEG-TS stream is the expected one
	require.Equal(t, testAudioConfig, normAudioConf(audTrack.Codec.(*tscodecs.MPEG4Audio).Config))

	// codec configuration records, updated at random-access boundaries only
	vidCfg := &remux.H264Config{}
	audCfg := &remux.MPEG4AudioConfig{}
	require.True(t, audCfg.Update(testAudioConfig))

	// clocks convert into the MP4 track time scales and map the first DTS
	// of each track to zero
	vidClock := remux.NewClock(videoTimeScale)
	audClock := remux.NewClock(audioTimeScale)

	type mp4VidSample struct {
		mdts      int64
		ptsOffset int32
		sync      bool
		payload   []byte
	}

	vidMP4 := make([]mp4VidSample, videoFrameNum)
	confChanges := 0

	for n, s := range vid {
		changed, err := vidCfg.Update(s.au)
		require.NoError(t, err)
		if changed {
			confChanges++
		}

		// strip in-band parameter sets: in MP4 they live in the init block
		var nalus [][]byte
		for _, nalu := range s.au {
			typ := h264.NALUType(nalu[0] & 0x1F)
			if typ != h264.NALUTypeSPS && typ != h264.NALUTypePPS {
				nalus = append(nalus, nalu)
			}
		}
		payload, err := h264.AVCC(nalus).Marshal()
		require.NoError(t, err)

		mdts, mpts := vidClock.FromUnified(s.dts, s.pts-s.dts)
		vidMP4[n] = mp4VidSample{
			mdts:      mdts,
			ptsOffset: int32(mpts - mdts),
			sync:      h264.IsRandomAccess(s.au),
			payload:   payload,
		}
	}

	// the configuration is set once and stays stable across GOPs
	require.Equal(t, 1, confChanges)

	type mp4AudSample struct {
		mdts    int64
		payload []byte
	}

	audMP4 := make([]mp4AudSample, audioFrameNum)
	for i, s := range aud {
		mdts, _ := audClock.FromUnified(s.pts, 0)
		audMP4[i] = mp4AudSample{mdts: mdts, payload: s.au}
	}

	// init block: codec configurations come from the records
	init := fmp4.Init{Tracks: []*fmp4.InitTrack{
		{
			ID:        1,
			TimeScale: videoTimeScale,
			Codec:     &mp4codecs.H264{SPS: vidCfg.SPS, PPS: vidCfg.PPS},
		},
		{
			ID:        2,
			TimeScale: audioTimeScale,
			Codec:     &mp4codecs.MPEG4Audio{Config: audCfg.Config},
		},
	}}

	var initBuf seekablebuffer.Buffer
	require.NoError(t, init.Marshal(&initBuf))

	// one segment per GOP
	var partsBuf seekablebuffer.Buffer
	for g := range gopCount {
		vidSeg := vidMP4[g*gopSize : (g+1)*gopSize]

		vidSamples := make([]*fmp4.Sample, gopSize)
		for j, s := range vidSeg {
			var dur int64
			if j+1 < gopSize {
				dur = vidSeg[j+1].mdts - s.mdts
			} else {
				dur = s.mdts - vidSeg[j-1].mdts
			}
			vidSamples[j] = &fmp4.Sample{
				Duration:        uint32(dur),
				PTSOffset:       s.ptsOffset,
				IsNonSyncSample: !s.sync,
				Payload:         s.payload,
			}
		}

		// audio samples whose decode time falls inside this segment
		segStart := int64(g) * audioTimeScale
		segEnd := segStart + audioTimeScale

		var audSeg []mp4AudSample
		for _, s := range audMP4 {
			if s.mdts >= segStart && s.mdts < segEnd {
				audSeg = append(audSeg, s)
			}
		}
		require.NotEmpty(t, audSeg)

		audSamples := make([]*fmp4.Sample, len(audSeg))
		for j, s := range audSeg {
			var dur int64
			if j+1 < len(audSeg) {
				dur = audSeg[j+1].mdts - s.mdts
			} else {
				dur = s.mdts - audSeg[j-1].mdts
			}
			audSamples[j] = &fmp4.Sample{
				Duration: uint32(dur),
				Payload:  s.payload,
			}
		}

		part := fmp4.Part{
			SequenceNumber: uint32(g + 1),
			Tracks: []*fmp4.PartTrack{
				{ID: 1, BaseTime: uint64(vidSeg[0].mdts), Samples: vidSamples},
				{ID: 2, BaseTime: uint64(audSeg[0].mdts), Samples: audSamples},
			},
		}
		require.NoError(t, part.Marshal(&partsBuf))
	}

	// assertions on the wire format

	var parts fmp4.Parts
	require.NoError(t, parts.Unmarshal(partsBuf.Bytes()))
	require.Len(t, parts, gopCount)

	expectedAudSegDur := []uint32{47 * 1024, 46 * 1024}

	var prevVidDTS int64 = -1
	var prevAudDTS int64 = -1
	var allVidPTS []int64

	for g, part := range parts {
		require.Len(t, part.Tracks, 2)

		// video track: exact segment duration, monotonic DTS
		vt := part.Tracks[0]
		require.Equal(t, uint64(g*90000), vt.BaseTime)
		require.Len(t, vt.Samples, gopSize)

		var vidSegDur uint32
		dts := int64(vt.BaseTime)
		for j, s := range vt.Samples {
			vidSegDur += s.Duration
			require.Equal(t, int64(g*gopSize+j)*frameDuration, dts)
			require.Greater(t, dts, prevVidDTS)
			prevVidDTS = dts

			pts := dts + int64(s.PTSOffset)
			require.GreaterOrEqual(t, pts, dts)
			allVidPTS = append(allVidPTS, pts)

			require.Equal(t, j != 0, s.IsNonSyncSample)

			dts += int64(s.Duration)
		}
		require.Equal(t, uint32(90000), vidSegDur)

		// audio track: exact segment duration, monotonic DTS
		at := part.Tracks[1]
		require.Equal(t, uint64([]int64{0, 47 * 1024}[g]), at.BaseTime)

		var audSegDur uint32
		adts := int64(at.BaseTime)
		for _, s := range at.Samples {
			audSegDur += s.Duration
			require.Greater(t, adts, prevAudDTS)
			prevAudDTS = adts
			require.Zero(t, s.PTSOffset)
			adts += int64(s.Duration)
		}
		require.Equal(t, expectedAudSegDur[g], audSegDur)
	}

	// PTS is monotonic in presentation order and matches the expected
	// reordered sequence exactly
	sortedPTS := slices.Clone(allVidPTS)
	slices.Sort(sortedPTS)
	require.IsIncreasing(t, sortedPTS)
	for k, pts := range sortedPTS {
		require.Equal(t, ptsDelay+int64(k)*frameDuration, pts)
	}

	vidHash, ok := vidCfg.Hash()
	require.True(t, ok)
	audHash, ok := audCfg.Hash()
	require.True(t, ok)

	//
	// fMP4 -> MPEG-TS
	//

	var init2 fmp4.Init
	require.NoError(t, init2.Unmarshal(bytes.NewReader(initBuf.Bytes())))
	require.Len(t, init2.Tracks, 2)

	require.Equal(t, uint32(videoTimeScale), init2.Tracks[0].TimeScale)
	require.Equal(t, uint32(audioTimeScale), init2.Tracks[1].TimeScale)

	// seed new configuration records from the init block (the out-of-band
	// configuration boundary)
	vidCodec, ok := init2.Tracks[0].Codec.(*mp4codecs.H264)
	require.True(t, ok)
	vidCfg2 := &remux.H264Config{}
	vidCfg2.Seed(vidCodec.SPS, vidCodec.PPS)

	audCodec, ok := init2.Tracks[1].Codec.(*mp4codecs.MPEG4Audio)
	require.True(t, ok)
	audCfg2 := &remux.MPEG4AudioConfig{}
	audCfg2.Update(normAudioConf(audCodec.Config))

	// the configuration survived the MPEG-TS -> fMP4 conversion
	vidHash2, ok := vidCfg2.Hash()
	require.True(t, ok)
	require.Equal(t, vidHash, vidHash2)
	audHash2, ok := audCfg2.Hash()
	require.True(t, ok)
	require.Equal(t, audHash, audHash2)

	vidClock2 := remux.NewClock(videoTimeScale)
	audClock2 := remux.NewClock(audioTimeScale)

	type outEvent struct {
		dts     int64
		pts     int64
		isVideo bool
		au      [][]byte
		payload []byte
	}

	var events []outEvent

	for _, part := range parts {
		// video
		mdts := int64(part.Tracks[0].BaseTime)
		for _, s := range part.Tracks[0].Samples {
			udts, upts := vidClock2.ToUnified(mdts, int64(s.PTSOffset))

			var au h264.AVCC
			require.NoError(t, au.Unmarshal(s.Payload))

			out := make([][]byte, 0, len(au)+2)
			if !s.IsNonSyncSample {
				// re-inject parameter sets from the configuration record
				out = append(out, vidCfg2.SPS, vidCfg2.PPS)
			}
			out = append(out, au...)

			events = append(events, outEvent{dts: udts, pts: upts, isVideo: true, au: out})
			mdts += int64(s.Duration)
		}

		// audio
		mdts = int64(part.Tracks[1].BaseTime)
		for _, s := range part.Tracks[1].Samples {
			udts, upts := audClock2.ToUnified(mdts, 0)
			events = append(events, outEvent{dts: udts, pts: upts, payload: s.Payload})
			mdts += int64(s.Duration)
		}
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].dts < events[j].dts })

	vidWTrack := &mpegts.Track{Codec: &tscodecs.H264{}}
	audWTrack := &mpegts.Track{Codec: &tscodecs.MPEG4Audio{Config: audCfg2.Config}}

	var tsBuf2 bytes.Buffer
	w2 := &mpegts.Writer{W: &tsBuf2, Tracks: []*mpegts.Track{vidWTrack, audWTrack}}
	require.NoError(t, w2.Initialize())

	for _, ev := range events {
		if ev.isVideo {
			require.NoError(t, w2.WriteH264(vidWTrack, ev.pts, ev.dts, ev.au))
		} else {
			require.NoError(t, w2.WriteMPEG4Audio(audWTrack, ev.pts, [][]byte{ev.payload}))
		}
	}

	// PCRs of the re-anchored stream are valid and near the time base
	requirePCRInRange(t, tsBuf2.Bytes(), gopCount*90000)

	// read the remuxed stream back

	vid2, aud2, _, audTrack2 := readTS(t, tsBuf2.Bytes())
	require.Len(t, vid2, videoFrameNum)
	require.Len(t, aud2, audioFrameNum)

	vidCfg3 := &remux.H264Config{}
	confChanges = 0
	prevVidDTS = -1

	for n, s := range vid2 {
		changed, err := vidCfg3.Update(s.au)
		require.NoError(t, err)
		if changed {
			confChanges++
		}

		// timestamps are anchored at the first DTS and match the source exactly
		require.Equal(t, int64(n)*frameDuration, s.dts)
		require.Equal(t, ptsDelay+int64(presIndex(n))*frameDuration, s.pts)
		require.Greater(t, s.dts, prevVidDTS)
		prevVidDTS = s.dts

		// access units survived the round trip, parameter sets included
		if presIndex(n)%gopSize == 0 {
			require.Equal(t, [][]byte{testRemuxH264SPS, testRemuxH264PPS, {5}}, s.au)
		} else {
			require.Equal(t, [][]byte{{1}}, s.au)
		}
	}
	require.Equal(t, 1, confChanges)

	var prevAudPTS int64 = -1
	for i, s := range aud2 {
		require.Equal(t, int64(i)*audioFrameDur, s.pts)
		require.Greater(t, s.pts, prevAudPTS)
		prevAudPTS = s.pts
		require.Equal(t, []byte{0x11, byte(i), 0x22}, s.au)
	}

	// the configuration survived the full round trip
	vidHash3, ok := vidCfg3.Hash()
	require.True(t, ok)
	require.Equal(t, vidHash, vidHash3)

	audCfg3 := &remux.MPEG4AudioConfig{}
	audCfg3.Update(normAudioConf(audTrack2.Codec.(*tscodecs.MPEG4Audio).Config))
	audHash3, ok := audCfg3.Hash()
	require.True(t, ok)
	require.Equal(t, audHash, audHash3)
}
