/***** BEGIN LICENSE BLOCK *****
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this file,
# You can obtain one at http://mozilla.org/MPL/2.0/.
#
# The Initial Developer of the Original Code is the Mozilla Foundation.
# Portions created by the Initial Developer are Copyright (C) 2012
# the Initial Developer. All Rights Reserved.
#
# Contributor(s):
#   Rob Miller (rmiller@mozilla.com)
#   Mike Trinkala (trink@mozilla.com)
#
# ***** END LICENSE BLOCK *****/

package file

import (
	"encoding/json"
	"fmt"
	. "github.com/mozilla-services/heka/pipeline"
	"github.com/mozilla-services/heka/plugins"
	"github.com/rafrombrc/go-notify"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var (
	FILEFORMATS = map[string]bool{
		"json":           true,
		"text":           true,
		"protobufstream": true,
	}

	TSFORMAT = "[2006/Jan/02:15:04:05 -0700] "
)

// Output plugin that writes message contents to a file on the file system.
type FileOutput struct {
	path          string
	format        string
	prefix_ts     bool
	perm          os.FileMode
	flushInterval uint32
	file          *os.File
	batchChan     chan []byte
	backChan      chan []byte
	folderPerm    os.FileMode
}

// ConfigStruct for FileOutput plugin.
type FileOutputConfig struct {
	// Full output file path.
	Path string

	// Format for message serialization, from text (payload only), json, or
	// protobufstream.
	Format string

	// Add timestamp prefix to each output line?
	Prefix_ts bool

	// Output file permissions (default "644").
	Perm string

	// Interval at which accumulated file data should be written to disk, in
	// milliseconds (default 1000, i.e. 1 second).
	FlushInterval uint32

	// Permissions to apply to directories created for FileOutput's
	// parent directory if it doesn't exist.  Must be a string
	// representation of an octal integer. Defaults to "700".
	FolderPerm string `toml:"folder_perm"`
}

func (o *FileOutput) ConfigStruct() interface{} {
	return &FileOutputConfig{
		Format:        "text",
		Perm:          "644",
		FlushInterval: 1000,
		FolderPerm:    "700",
	}
}

func (o *FileOutput) Init(config interface{}) (err error) {
	conf := config.(*FileOutputConfig)
	if _, ok := FILEFORMATS[conf.Format]; !ok {
		err = fmt.Errorf("FileOutput '%s' unsupported format: %s", conf.Path,
			conf.Format)
		return
	}
	o.path = conf.Path
	o.format = conf.Format
	o.prefix_ts = conf.Prefix_ts
	var intPerm int64

	if intPerm, err = strconv.ParseInt(conf.FolderPerm, 8, 32); err != nil {
		err = fmt.Errorf("FileOutput '%s' can't parse `folder_perm`, is it an octal integer string?",
			o.path)
		return
	}
	o.folderPerm = os.FileMode(intPerm)

	if intPerm, err = strconv.ParseInt(conf.Perm, 8, 32); err != nil {
		err = fmt.Errorf("FileOutput '%s' can't parse `perm`, is it an octal integer string?",
			o.path)
		return
	}
	o.perm = os.FileMode(intPerm)
	if err = o.openFile(); err != nil {
		err = fmt.Errorf("FileOutput '%s' error opening file: %s", o.path, err)
		return
	}

	o.flushInterval = conf.FlushInterval
	o.batchChan = make(chan []byte)
	o.backChan = make(chan []byte, 2) // Never block on the hand-back
	return
}

func (o *FileOutput) openFile() (err error) {
	basePath := filepath.Dir(o.path)
	if err = os.MkdirAll(basePath, o.folderPerm); err != nil {
		return fmt.Errorf("Can't create the basepath for the FileOutput plugin: %s", err.Error())
	}
	if err = plugins.CheckWritePermission(basePath); err != nil {
		return
	}
	o.file, err = os.OpenFile(o.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, o.perm)
	return
}

func (o *FileOutput) Run(or OutputRunner, h PluginHelper) (err error) {
	var wg sync.WaitGroup
	wg.Add(2)
	go o.receiver(or, &wg)
	go o.committer(or, &wg)
	wg.Wait()
	return
}

// Runs in a separate goroutine, accepting incoming messages, buffering output
// data until the ticker triggers the buffered data should be put onto the
// committer channel.
func (o *FileOutput) receiver(or OutputRunner, wg *sync.WaitGroup) {
	var pack *PipelinePack
	var e error
	ok := true
	ticker := time.Tick(time.Duration(o.flushInterval) * time.Millisecond)
	outBatch := make([]byte, 0, 10000)
	outBytes := make([]byte, 0, 1000)
	inChan := or.InChan()

	for ok {
		select {
		case pack, ok = <-inChan:
			if !ok {
				// Closed inChan => we're shutting down, flush data
				if len(outBatch) > 0 {
					o.batchChan <- outBatch
				}
				close(o.batchChan)
				break
			}
			if e = o.handleMessage(pack, &outBytes); e != nil {
				or.LogError(e)
			} else {
				outBatch = append(outBatch, outBytes...)
			}
			outBytes = outBytes[:0]
			pack.Recycle()
		case <-ticker:
			if len(outBatch) > 0 {
				// This will block until the other side is ready to accept
				// this batch, freeing us to start on the next one.
				o.batchChan <- outBatch
				outBatch = <-o.backChan
			}
		}
	}
	wg.Done()
}

// Performs the actual task of extracting data from the pack and writing it
// into the output buffer in the proper format.
func (o *FileOutput) handleMessage(pack *PipelinePack, outBytes *[]byte) (err error) {
	if o.prefix_ts && o.format != "protobufstream" {
		ts := time.Now().Format(TSFORMAT)
		*outBytes = append(*outBytes, ts...)
	}
	switch o.format {
	case "json":
		if jsonMessage, err := json.Marshal(pack.Message); err == nil {
			*outBytes = append(*outBytes, jsonMessage...)
			*outBytes = append(*outBytes, NEWLINE)
		} else {
			err = fmt.Errorf("Can't encode to JSON: %s", err)
		}
	case "text":
		*outBytes = append(*outBytes, *pack.Message.Payload...)
		*outBytes = append(*outBytes, NEWLINE)
	case "protobufstream":
		if err = ProtobufEncodeMessage(pack, &*outBytes); err != nil {
			err = fmt.Errorf("Can't encode to ProtoBuf: %s", err)
		}
	default:
		err = fmt.Errorf("Invalid serialization format %s", o.format)
	}
	return
}

// Runs in a separate goroutine, waits for buffered data on the committer
// channel, writes it out to the filesystem, and puts the now empty buffer on
// the return channel for reuse.
func (o *FileOutput) committer(or OutputRunner, wg *sync.WaitGroup) {
	initBatch := make([]byte, 0, 10000)
	o.backChan <- initBatch
	var outBatch []byte
	var err error

	ok := true
	hupChan := make(chan interface{})
	notify.Start(RELOAD, hupChan)

	for ok {
		select {
		case outBatch, ok = <-o.batchChan:
			if !ok {
				// Channel is closed => we're shutting down, exit cleanly.
				break
			}
			n, err := o.file.Write(outBatch)
			if err != nil {
				or.LogError(fmt.Errorf("Can't write to %s: %s", o.path, err))
			} else if n != len(outBatch) {
				or.LogError(fmt.Errorf("Truncated output for %s", o.path))
			} else {
				o.file.Sync()
			}
			outBatch = outBatch[:0]
			o.backChan <- outBatch
		case <-hupChan:
			o.file.Close()
			if err = o.openFile(); err != nil {
				// TODO: Need a way to handle this gracefully, see
				// https://github.com/mozilla-services/heka/issues/38
				panic(fmt.Sprintf("FileOutput unable to reopen file '%s': %s",
					o.path, err))
			}
		}
	}

	o.file.Close()
	wg.Done()
}

func init() {
	RegisterPlugin("FileOutput", func() interface{} {
		return new(FileOutput)
	})
}
