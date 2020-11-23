// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package decoder

import (
	"bytes"
	"regexp"
	"time"

	"github.com/DataDog/datadog-agent/pkg/util/log"
)

// truncatedFlag is the flag that is added at the beginning
// or/and at the end of every trucated lines.
var truncatedFlag = []byte("...TRUNCATED...")

// escapedLineFeed is used to escape new line character
// for multiline message.
// New line character needs to be escaped because they are used
// as delimiter for transport.
var escapedLineFeed = []byte(`\n`)

// LineHandler handles raw lines to form structured lines
type LineHandler interface {
	Handle(input *Message)
	Start()
	Stop()
}

// SingleLineHandler takes care of tracking the line length
// and truncating them when they are too long.
type SingleLineHandler struct {
	inputChan      chan *Message
	outputChan     chan *Message
	shouldTruncate bool
	lineLimit      int
}

// NewSingleLineHandler returns a new SingleLineHandler.
func NewSingleLineHandler(outputChan chan *Message, lineLimit int) *SingleLineHandler {
	return &SingleLineHandler{
		inputChan:  make(chan *Message),
		outputChan: outputChan,
		lineLimit:  lineLimit,
	}
}

// Handle puts all new lines into a channel for later processing.
func (h *SingleLineHandler) Handle(input *Message) {
	h.inputChan <- input
}

// Stop stops the handler.
func (h *SingleLineHandler) Stop() {
	close(h.inputChan)
}

// Start starts the handler.
func (h *SingleLineHandler) Start() {
	go h.run()
}

// run consumes new lines and processes them.
func (h *SingleLineHandler) run() {
	for line := range h.inputChan {
		h.process(line)
	}
	close(h.outputChan)
}

// process transforms a raw line into a structured line,
// it guarantees that the content of the line won't exceed
// the limit and that the length of the line is properly tracked
// so that the agent restarts tailing from the right place.
func (h *SingleLineHandler) process(message *Message) {
	isTruncated := h.shouldTruncate
	h.shouldTruncate = false

	message.Content = bytes.TrimSpace(message.Content)

	if isTruncated {
		// the previous line has been truncated because it was too long,
		// the new line is just a remainder,
		// adding the truncated flag at the beginning of the content
		message.Content = append(truncatedFlag, message.Content...)
	}

	if len(message.Content) < h.lineLimit {
		h.outputChan <- message
	} else {
		// the line is too long, it needs to be cut off and send,
		// adding the truncated flag the end of the content
		message.Content = append(message.Content, truncatedFlag...)
		h.outputChan <- message
		// make sure the following part of the line will be cut off as well
		h.shouldTruncate = true
	}
}

// defaultFlushTimeout represents the time after which a multiline
// will be be considered as complete.
const defaultFlushTimeout = 1000 * time.Millisecond

// MultiLineHandler makes sure that multiple lines from a same content
// are properly put together.
type MultiLineHandler struct {
	inputChan      chan *Message
	outputChan     chan *Message
	newContentRe   *regexp.Regexp
	buffer         *bytes.Buffer
	flushTimeout   time.Duration
	lineLimit      int
	shouldTruncate bool
	linesLen       int
	status         string
	timestamp      string
}

// NewMultiLineHandler returns a new MultiLineHandler.
func NewMultiLineHandler(outputChan chan *Message, newContentRe *regexp.Regexp, flushTimeout time.Duration, lineLimit int) *MultiLineHandler {
	return &MultiLineHandler{
		inputChan:    make(chan *Message),
		outputChan:   outputChan,
		newContentRe: newContentRe,
		buffer:       bytes.NewBuffer(nil),
		flushTimeout: flushTimeout,
		lineLimit:    lineLimit,
	}
}

// Handle forward lines to lineChan to process them.
func (h *MultiLineHandler) Handle(input *Message) {
	h.inputChan <- input
}

// Stop stops the handler.
func (h *MultiLineHandler) Stop() {
	close(h.inputChan)
}

// Start starts the handler.
func (h *MultiLineHandler) Start() {
	go h.run()
}

// run processes new lines from the channel and makes sur the content is properly sent when
// it stayed for too long in the buffer.
func (h *MultiLineHandler) run() {
	flushTimer := time.NewTimer(h.flushTimeout)
	defer func() {
		flushTimer.Stop()
		// make sure the content stored in the buffer gets sent,
		// this can happen when the stop is called in between two timer ticks.
		h.sendBuffer()
		close(h.outputChan)
	}()
	for {
		select {
		case message, isOpen := <-h.inputChan:
			if !isOpen {
				// lineChan has been closed, no more lines are expected
				return
			}
			// process the new line and restart the timeout
			if !flushTimer.Stop() {
				// timer stop doesn't not prevent the timer to tick,
				// makes sure the event is consumed to avoid sending
				// just one piece of the content.
				select {
				case <-flushTimer.C:
				default:
				}
			}
			h.process(message)
			flushTimer.Reset(h.flushTimeout)
		case <-flushTimer.C:
			// no line has been collected since a while,
			// the content is supposed to be complete.
			h.sendBuffer()
		}
	}
}

// process aggregates multiple lines to form a full multiline message,
// it stops when a line matches with the new content regular expression.
// It also makes sure that the content will never exceed the limit
// and that the length of the lines is properly tracked
// so that the agent restarts tailing from the right place.
func (h *MultiLineHandler) process(message *Message) {

	if h.newContentRe.Match(message.Content) {
		// the current line is part of a new message,
		// send the buffer
		h.sendBuffer()
	}

	isTruncated := h.shouldTruncate
	h.shouldTruncate = false

	// track the raw data length and the timestamp so that the agent tails
	// from the right place at restart
	h.linesLen += message.RawDataLen
	h.timestamp = message.Timestamp
	h.status = message.Status

	if h.buffer.Len() > 0 {
		// the buffer already contains some data which means that
		// the current line is not the first line of the message
		h.buffer.Write(escapedLineFeed)
	}

	if isTruncated {
		// the previous line has been truncated because it was too long,
		// the new line is just a remainder,
		// adding the truncated flag at the beginning of the content
		h.buffer.Write(truncatedFlag)
	}

	h.buffer.Write(message.Content)

	if h.buffer.Len() >= h.lineLimit {
		// the multiline message is too long, it needs to be cut off and send,
		// adding the truncated flag the end of the content
		h.buffer.Write(truncatedFlag)
		h.sendBuffer()
		h.shouldTruncate = true
	}
}

// sendBuffer forwards the content stored in the buffer
// to the output channel.
func (h *MultiLineHandler) sendBuffer() {
	defer func() {
		h.buffer.Reset()
		h.linesLen = 0
		h.shouldTruncate = false
	}()

	data := bytes.TrimSpace(h.buffer.Bytes())
	content := make([]byte, len(data))
	copy(content, data)

	if len(content) > 0 || h.linesLen > 0 {
		h.outputChan <- NewMessage(content, h.status, h.linesLen, h.timestamp)
	}
}

// AutoMultilineHandler can switch from single to multiline handler if upon the occurrence
// of a stable pattern at the begginning of the process
type AutoMultilineHandler struct {
	multiLineHandler  *MultiLineHandler
	singleLineHandler *SingleLineHandler
	inputChan         chan *Message
	outputChan        chan *Message
	flipChan          chan struct{}
	linesToAssess     int
	linesTested       int
	lineLimit         int
	potentialRegexp   []*regexp.Regexp
	processsingFunc   func(message *Message)
}

// NewAutoMultilineHandler returns a new SingleLineHandler.
func NewAutoMultilineHandler(outputChan chan *Message, lineLimit, linesToAssess int) *AutoMultilineHandler {
	h := &AutoMultilineHandler{
		inputChan:       make(chan *Message),
		outputChan:      outputChan,
		flipChan:        make(chan struct{}, 1),
		lineLimit:       lineLimit,
		potentialRegexp: formatsToTry,
		linesToAssess:   linesToAssess,
	}

	h.singleLineHandler = NewSingleLineHandler(outputChan, lineLimit)
	h.processsingFunc = h.processAndTry

	return h
}

// Handle puts all new lines into a channel for later processing.
func (h *AutoMultilineHandler) Handle(input *Message) {
	h.inputChan <- input
}

// Stop stops the handler.
func (h *AutoMultilineHandler) Stop() {
	close(h.inputChan)
}

// Start starts the handler.
func (h *AutoMultilineHandler) Start() {
	go h.run()
}

// run consumes new lines and processes them.
func (h *AutoMultilineHandler) run() {
	for {
		select {
		case <-h.flipChan:
			return
		case line, isOpen := <-h.inputChan:
			if !isOpen {
				close(h.outputChan)
				return
			}
			h.processsingFunc(line)
		}
	}
}

func (h *AutoMultilineHandler) processAndTry(message *Message) {
	// Process message before anything else
	h.singleLineHandler.process(message)

	workingRegexp := []*regexp.Regexp{}
	for _, r := range h.potentialRegexp {
		match := r.Match(message.Content)
		if match {
			log.Tracef("A regexp matched during multi-line auto sensing: %v", r)
			workingRegexp = append(workingRegexp, r)
		}
	}

	if len(workingRegexp) == 0 {
		// TODO per regexp matching count, here we exit as soon as a line just don't match any regexp
		log.Debug("No matching pattern found during multi-line autosensing")
		// Stay with the single line handler
		h.processsingFunc = h.singleLineHandler.process
		return
	}

	h.potentialRegexp = workingRegexp

	if h.linesTested++; h.linesTested == h.linesToAssess {
		// TODO: support score / tolerate some matching failure
		// score := float32(h.linesMatching) / float32(h.linesTested)
		// if score > threshold ....
		log.Debug("At least one pattern matched all sampled lines")
		h.switchToMultilineHandler(workingRegexp[0])
	} else {
		h.potentialRegexp = workingRegexp
	}
}

func (h *AutoMultilineHandler) switchToMultilineHandler(r *regexp.Regexp) {
	// Cleanup interim logic
	h.flipChan <- struct{}{}
	h.singleLineHandler = nil

	// Build & start a multiline-handler
	h.multiLineHandler = &MultiLineHandler{
		inputChan:    h.inputChan,
		outputChan:   h.outputChan,
		newContentRe: r,
		buffer:       bytes.NewBuffer(nil),
		flushTimeout: defaultFlushTimeout,
		lineLimit:    h.lineLimit,
	}
	h.multiLineHandler.Start()
}

// Savegely grabbed from https://github.com/egnyte/ax/blob/master/pkg/heuristic/timestamp.go
// TODO: Update these
var formatsToTry []*regexp.Regexp = []*regexp.Regexp{
	// time.RFC3339,
	regexp.MustCompile(`\d+-\d+-\d+T\d+:\d+:\d+(\.\d+)?(Z\d*:?\d*)?`),
	// time.ANSIC,
	regexp.MustCompile(`[A-Za-z_]+ [A-Za-z_]+ +\d+ \d+:\d+:\d+ \d+`),
	// time.UnixDate,
	regexp.MustCompile(`[A-Za-z_]+ [A-Za-z_]+ +\d+ \d+:\d+:\d+( [A-Za-z_]+ \d+)?`),
	// time.RubyDate,
	regexp.MustCompile(`[A-Za-z_]+ [A-Za-z_]+ \d+ \d+:\d+:\d+ [\-\+]\d+ \d+`),
	// time.RFC822,
	regexp.MustCompile(`\d+ [A-Za-z_]+ \d+ \d+:\d+ [A-Za-z_]+`),
	// time.RFC822Z,
	regexp.MustCompile(`\d+ [A-Za-z_]+ \d+ \d+:\d+ -\d+`),
	// time.RFC850,
	regexp.MustCompile(`[A-Za-z_]+, \d+-[A-Za-z_]+-\d+ \d+:\d+:\d+ [A-Za-z_]+`),
	// time.RFC1123,
	regexp.MustCompile(`[A-Za-z_]+, \d+ [A-Za-z_]+ \d+ \d+:\d+:\d+ [A-Za-z_]+`),
	// time.RFC1123Z,
	regexp.MustCompile(`[A-Za-z_]+, \d+ [A-Za-z_]+ \d+ \d+:\d+:\d+ -\d+`),
	// time.RFC3339Nano,
	regexp.MustCompile(`\d+-\d+-\d+[A-Za-z_]+\d+:\d+:\d+\.\d+[A-Za-z_]+\d+:\d+`),
	// "2006-01-02 15:04:05",
	regexp.MustCompile(`\d+-\d+-\d+ \d+:\d+:\d+(,\d+)?`),
}
