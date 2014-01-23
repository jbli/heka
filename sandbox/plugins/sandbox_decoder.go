/***** BEGIN LICENSE BLOCK *****
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this file,
# You can obtain one at http://mozilla.org/MPL/2.0/.
#
# The Initial Developer of the Original Code is the Mozilla Foundation.
# Portions created by the Initial Developer are Copyright (C) 2013
# the Initial Developer. All Rights Reserved.
#
# Contributor(s):
#   Mike Trinkala (trink@mozilla.com)
#
# ***** END LICENSE BLOCK *****/

package plugins

import (
	"code.google.com/p/goprotobuf/proto"
	"errors"
	"fmt"
	"github.com/mozilla-services/heka/message"
	"github.com/mozilla-services/heka/pipeline"
	. "github.com/mozilla-services/heka/sandbox"
	"github.com/mozilla-services/heka/sandbox/lua"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// Decoder for converting structured/unstructured data into Heka messages.
type SandboxDecoder struct {
	sb                     Sandbox
	sbc                    *SandboxConfig
	processMessageCount    int64
	processMessageFailures int64
	processMessageSamples  int64
	processMessageDuration int64
	reportLock             sync.Mutex
	sample                 bool
	err                    error
	pack                   *pipeline.PipelinePack
	packs                  []*pipeline.PipelinePack
	dRunner                pipeline.DecoderRunner
}

func (pd *SandboxDecoder) ConfigStruct() interface{} {
	return &SandboxConfig{
		ModuleDirectory:  pipeline.GetHekaConfigDir("lua_modules"),
		MemoryLimit:      8 * 1024 * 1024,
		InstructionLimit: 1e6,
		OutputLimit:      63 * 1024,
	}
}

func (s *SandboxDecoder) Init(config interface{}) (err error) {
	if s.sb != nil {
		return // no-op already initialized
	}
	s.sbc = config.(*SandboxConfig)
	s.sbc.ScriptFilename = pipeline.GetHekaConfigDir(s.sbc.ScriptFilename)
	s.sample = true

	switch s.sbc.ScriptType {
	case "lua":
		s.sb, err = lua.CreateLuaSandbox(s.sbc)
		if err != nil {
			return
		}
	default:
		return fmt.Errorf("unsupported script type: %s", s.sbc.ScriptType)
	}
	err = s.sb.Init("", "decoder")
	return
}

func copyMessageHeaders(dst *message.Message, src *message.Message) {
	if src == nil || dst == nil || src == dst {
		return
	}

	if cap(src.Uuid) > 0 {
		dst.SetUuid(src.Uuid)
	} else {
		dst.Uuid = nil
	}
	if src.Timestamp != nil {
		dst.SetTimestamp(*src.Timestamp)
	} else {
		dst.Timestamp = nil
	}
	if src.Type != nil {
		dst.SetType(*src.Type)
	} else {
		dst.Type = nil
	}
	if src.Logger != nil {
		dst.SetLogger(*src.Logger)
	} else {
		dst.Logger = nil
	}
	if src.Severity != nil {
		dst.SetSeverity(*src.Severity)
	} else {
		dst.Severity = nil
	}
	if src.Pid != nil {
		dst.SetPid(*src.Pid)
	} else {
		dst.Pid = nil
	}
	if src.Hostname != nil {
		dst.SetHostname(*src.Hostname)
	} else {
		dst.Hostname = nil
	}
}

func (s *SandboxDecoder) SetDecoderRunner(dr pipeline.DecoderRunner) {
	s.dRunner = dr
	var original *message.Message

	s.sb.InjectMessage(func(payload, payload_type, payload_name string) int {
		if s.pack == nil {
			s.pack = dr.NewPack()
			if original == nil && len(s.packs) > 0 {
				original = s.packs[0].Message // payload injections have the original header data in the first pack
			}
		} else {
			original = nil // processing a new message, clear the old message
		}
		if len(payload_type) == 0 { // heka protobuf message
			if original == nil {
				original = new(message.Message)
				copyMessageHeaders(original, s.pack.Message) // save off the header values since unmarshal will wipe them out
			}
			if nil != proto.Unmarshal([]byte(payload), s.pack.Message) {
				return 1
			}
		} else {
			s.pack.Message.SetPayload(payload)
			ptype, _ := message.NewField("payload_type", payload_type, "file-extension")
			s.pack.Message.AddField(ptype)
			pname, _ := message.NewField("payload_name", payload_name, "")
			s.pack.Message.AddField(pname)
		}
		if original != nil {
			// if future injections fail to set the standard headers, use the values
			// from the original message.
			if s.pack.Message.Uuid == nil {
				s.pack.Message.SetUuid(original.GetUuid())
			}
			if s.pack.Message.Timestamp == nil {
				s.pack.Message.SetTimestamp(original.GetTimestamp())
			}
			if s.pack.Message.Type == nil {
				s.pack.Message.SetType(original.GetType())
			}
			if s.pack.Message.Hostname == nil {
				s.pack.Message.SetHostname(original.GetHostname())
			}
			if s.pack.Message.Logger == nil {
				s.pack.Message.SetLogger(original.GetLogger())
			}
			if s.pack.Message.Severity == nil {
				s.pack.Message.SetSeverity(original.GetSeverity())
			}
			if s.pack.Message.Pid == nil {
				s.pack.Message.SetPid(original.GetPid())
			}
		}
		s.packs = append(s.packs, s.pack)
		s.pack = nil
		return 0
	})
}

func (s *SandboxDecoder) Shutdown() {
	if s.sb != nil {
		s.sb.Destroy("")
		s.sb = nil
	}
}

func (s *SandboxDecoder) Decode(pack *pipeline.PipelinePack) (packs []*pipeline.PipelinePack, err error) {
	if s.sb == nil {
		err = s.err
		return
	}
	s.pack = pack
	atomic.AddInt64(&s.processMessageCount, 1)

	var startTime time.Time
	if s.sample {
		startTime = time.Now()
	}
	retval := s.sb.ProcessMessage(s.pack)
	if s.sample {
		duration := time.Since(startTime).Nanoseconds()
		s.reportLock.Lock()
		s.processMessageDuration += duration
		s.processMessageSamples++
		s.reportLock.Unlock()
	}
	s.sample = 0 == rand.Intn(pipeline.DURATION_SAMPLE_DENOMINATOR)
	if retval > 0 {
		s.err = errors.New("FATAL: " + s.sb.LastError())
		s.dRunner.LogError(s.err)
		pipeline.Globals().ShutDown()
	}
	if retval < 0 {
		atomic.AddInt64(&s.processMessageFailures, 1)
		s.err = fmt.Errorf("Failed parsing: %s", s.pack.Message.GetPayload())
		if len(s.packs) > 1 {
			for _, p := range s.packs[1:] {
				p.Recycle()
			}
		}
		s.packs = nil
	}
	packs = s.packs
	s.packs = nil
	err = s.err
	return
}

// Satisfies the `pipeline.ReportingPlugin` interface to provide sandbox state
// information to the Heka report and dashboard.
func (s *SandboxDecoder) ReportMsg(msg *message.Message) error {
	if s.sb == nil {
		return fmt.Errorf("Decoder is not running")
	}
	s.reportLock.Lock()
	defer s.reportLock.Unlock()

	message.NewIntField(msg, "Memory", int(s.sb.Usage(TYPE_MEMORY,
		STAT_CURRENT)), "B")
	message.NewIntField(msg, "MaxMemory", int(s.sb.Usage(TYPE_MEMORY,
		STAT_MAXIMUM)), "B")
	message.NewIntField(msg, "MaxInstructions", int(s.sb.Usage(
		TYPE_INSTRUCTIONS, STAT_MAXIMUM)), "count")
	message.NewIntField(msg, "MaxOutput", int(s.sb.Usage(TYPE_OUTPUT,
		STAT_MAXIMUM)), "B")
	message.NewInt64Field(msg, "ProcessMessageCount", atomic.LoadInt64(&s.processMessageCount), "count")
	message.NewInt64Field(msg, "ProcessMessageFailures", atomic.LoadInt64(&s.processMessageFailures), "count")
	message.NewInt64Field(msg, "ProcessMessageSamples", s.processMessageSamples, "count")

	var tmp int64 = 0
	if s.processMessageSamples > 0 {
		tmp = s.processMessageDuration / s.processMessageSamples
	}
	message.NewInt64Field(msg, "ProcessMessageAvgDuration", tmp, "ns")

	return nil
}

func init() {
	pipeline.RegisterPlugin("SandboxDecoder", func() interface{} {
		return new(SandboxDecoder)
	})
	pipeline.RegisterPlugin("SandboxFilter", func() interface{} {
		return new(SandboxFilter)
	})
	pipeline.RegisterPlugin("SandboxManagerFilter", func() interface{} {
		return new(SandboxManagerFilter)
	})
}
