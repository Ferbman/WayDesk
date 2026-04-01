package capture

import (
	"fmt"
	"log/slog"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
)

// Pipeline manages the GStreamer capture and encoding pipeline.
type Pipeline struct {
	pipeline *gst.Pipeline
	appSink  *app.Sink
	logger   *slog.Logger
}

// NewPipeline creates a new GStreamer pipeline reading from PipeWire via FD
// and encoding to H.264 using NVENC for zero-latency streaming.
func NewPipeline(fd int, nodeID uint32, logger *slog.Logger) (*Pipeline, error) {
	// Initialize GStreamer. Safe to call multiple times.
	gst.Init(nil)

	// Construction of the pipeline:
	// 1. pipewiresrc: Reads Wayland screen stream
	// 2. video/x-raw,max-framerate=60/1: Keep the native frame pacing
	// 3. nvh264enc: Hardware encode with profile=baseline for WebRTC compatibility
	// 4. appsink: Yields the raw encoded byte slices with sync=false for zero-delay pulling
	pipelineStr := fmt.Sprintf(
		"pipewiresrc fd=%d path=%d ! "+
			"video/x-raw,max-framerate=60/1 ! "+
			"nvh264enc preset=low-latency rc-mode=cbr bitrate=2500 zerolatency=true gop-size=60 ! "+
			"video/x-h264,profile=baseline ! "+
			"h264parse config-interval=-1 ! "+
			"video/x-h264,stream-format=byte-stream,alignment=au ! "+
			"appsink name=webrtc-sink max-buffers=1 drop=true sync=false",
		fd, nodeID,
	)

	logger.Info("creating gstreamer pipeline", "pipeline", pipelineStr)

	pipeline, err := gst.NewPipelineFromString(pipelineStr)
	if err != nil {
		return nil, fmt.Errorf("failed to create pipeline: %w", err)
	}

	// Retrieve the appsink element by name.
	sinkElement, err := pipeline.GetElementByName("webrtc-sink")
	if err != nil {
		return nil, fmt.Errorf("failed to find appsink: %w", err)
	}

	appSink := app.SinkFromElement(sinkElement)

	p := &Pipeline{
		pipeline: pipeline,
		appSink:  appSink,
		logger:   logger,
	}

	return p, nil
}

// SetOnSample registers a callback to receive H.264 NAL units as they are encoded.
func (p *Pipeline) SetOnSample(onSample func([]byte)) {
	p.appSink.SetCallbacks(&app.SinkCallbacks{
		NewSampleFunc: func(sink *app.Sink) gst.FlowReturn {
			sample := sink.PullSample()
			if sample == nil {
				return gst.FlowEOS
			}

			buffer := sample.GetBuffer()
			if buffer == nil {
				return gst.FlowError
			}

			// Map the buffer to read its bytes
			mapInfo := buffer.Map(gst.MapRead)
			defer buffer.Unmap()

			// Create a copy of the bytes to ensure memory safety when passing it
			// out of the GStreamer thread.
			data := make([]byte, mapInfo.Size())
			copy(data, mapInfo.Bytes())

			onSample(data)

			return gst.FlowOK
		},
	})
}

// Start begins the GStreamer pipeline execution.
func (p *Pipeline) Start() error {
	p.logger.Info("starting gstreamer pipeline")
	return p.pipeline.SetState(gst.StatePlaying)
}

// Stop ends the pipeline and cleans up resources.
func (p *Pipeline) Stop() error {
	p.logger.Info("stopping gstreamer pipeline")
	return p.pipeline.SetState(gst.StateNull)
}
