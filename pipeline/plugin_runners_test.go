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
#   Victor Ng (vng@mozilla.com)
#   Mike Trinkala (trink@mozilla.com)
#
# ***** END LICENSE BLOCK *****/

package pipeline

import (
	"code.google.com/p/gomock/gomock"
	"errors"
	ts "github.com/mozilla-services/heka/pipeline/testsupport"
	gs "github.com/rafrombrc/gospec/src/gospec"
	"sync"
)

var stopinputTimes int

type StoppingInput struct{}

func (s *StoppingInput) Init(config interface{}) (err error) {
	if stopinputTimes > 1 {
		err = errors.New("Stopped enough, done")
	}
	return
}

func (s *StoppingInput) Run(ir InputRunner, h PluginHelper) (err error) {
	return
}

func (s *StoppingInput) CleanupForRestart() {
	stopinputTimes += 1
}

func (s *StoppingInput) Stop() {
	return
}

func InputRunnerSpec(c gs.Context) {
	t := &ts.SimpleT{}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	globals := &GlobalConfigStruct{
		PluginChanSize: 5,
	}
	NewPipelineConfig(globals)

	mockHelper := NewMockPluginHelper(ctrl)

	c.Specify("Runner restarts a plugin on the first time only", func() {
		var pluginGlobals PluginGlobals
		pluginGlobals.Retries = RetryOptions{
			MaxDelay:   "1us",
			Delay:      "1us",
			MaxJitter:  "1us",
			MaxRetries: 1,
		}
		pc := new(PipelineConfig)
		pc.inputWrappers = make(map[string]*PluginWrapper)

		pw := &PluginWrapper{
			Name:          "stopping",
			ConfigCreator: func() interface{} { return nil },
			PluginCreator: func() interface{} { return new(StoppingInput) },
		}
		pc.inputWrappers["stopping"] = pw

		input := new(StoppingInput)
		iRunner := NewInputRunner("stopping", input, &pluginGlobals)
		var wg sync.WaitGroup
		cfgCall := mockHelper.EXPECT().PipelineConfig().Times(7)
		cfgCall.Return(pc)
		wg.Add(1)
		iRunner.Start(mockHelper, &wg)
		wg.Wait()
		c.Expect(stopinputTimes, gs.Equals, 2)
	})
}

var stopoutputTimes int

type StoppingOutput struct{}

func (s *StoppingOutput) Init(config interface{}) (err error) {
	if stopoutputTimes > 1 {
		err = errors.New("exiting now")
	}
	return
}

func (s *StoppingOutput) Run(or OutputRunner, h PluginHelper) (err error) {
	return
}

func (s *StoppingOutput) CleanupForRestart() {
	stopoutputTimes += 1
}

func (s *StoppingOutput) Stop() {
	return
}

var (
	stopresumeHolder   []string      = make([]string, 0, 10)
	pack               *PipelinePack = new(PipelinePack)
	stopresumerunTimes int
)

type StopResumeOutput struct{}

func (s *StopResumeOutput) Init(config interface{}) (err error) {
	if stopresumerunTimes > 2 {
		err = errors.New("Aborting")
	}
	return
}

func (s *StopResumeOutput) Run(or OutputRunner, h PluginHelper) (err error) {
	if stopresumerunTimes == 0 {
		or.RetainPack(pack)
	} else if stopresumerunTimes == 1 {
		inChan := or.InChan()
		pk := <-inChan
		if pk == pack {
			stopresumeHolder = append(stopresumeHolder, "success")
		}
	} else if stopresumerunTimes > 1 {
		inChan := or.InChan()
		select {
		case <-inChan:
			stopresumeHolder = append(stopresumeHolder, "oye")
		default:
			stopresumeHolder = append(stopresumeHolder, "woot")
		}
	}
	stopresumerunTimes += 1
	return
}

func (s *StopResumeOutput) CleanupForRestart() {
}

func (s *StopResumeOutput) Stop() {
	return
}

func OutputRunnerSpec(c gs.Context) {
	t := new(ts.SimpleT)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockHelper := NewMockPluginHelper(ctrl)

	c.Specify("Runner restarts a plugin on the first time only", func() {
		pc := new(PipelineConfig)
		var pluginGlobals PluginGlobals
		pluginGlobals.Retries = RetryOptions{
			MaxDelay:   "1us",
			Delay:      "1us",
			MaxJitter:  "1us",
			MaxRetries: 1,
		}
		pw := &PluginWrapper{
			Name:          "stoppingOutput",
			ConfigCreator: func() interface{} { return nil },
			PluginCreator: func() interface{} { return new(StoppingOutput) },
		}
		output := new(StoppingOutput)
		pc.outputWrappers = make(map[string]*PluginWrapper)
		pc.outputWrappers["stoppingOutput"] = pw
		oRunner := NewFORunner("stoppingOutput", output, &pluginGlobals)
		var wg sync.WaitGroup
		cfgCall := mockHelper.EXPECT().PipelineConfig()
		cfgCall.Return(pc)
		wg.Add(1)
		oRunner.Start(mockHelper, &wg) // no panic => success
		wg.Wait()
		c.Expect(stopoutputTimes, gs.Equals, 2)
	})

	c.Specify("Runner restarts plugin and resumes feeding it", func() {
		pc := new(PipelineConfig)
		var pluginGlobals PluginGlobals
		pluginGlobals.Retries = RetryOptions{
			MaxDelay:   "1us",
			Delay:      "1us",
			MaxJitter:  "1us",
			MaxRetries: 4,
		}
		pw := &PluginWrapper{
			Name:          "stoppingresumeOutput",
			ConfigCreator: func() interface{} { return nil },
			PluginCreator: func() interface{} { return new(StopResumeOutput) },
		}
		output := new(StopResumeOutput)
		pc.outputWrappers = make(map[string]*PluginWrapper)
		pc.outputWrappers["stoppingresumeOutput"] = pw
		oRunner := NewFORunner("stoppingresumeOutput", output, &pluginGlobals)
		var wg sync.WaitGroup
		cfgCall := mockHelper.EXPECT().PipelineConfig()
		cfgCall.Return(pc)
		wg.Add(1)
		oRunner.Start(mockHelper, &wg) // no panic => success
		wg.Wait()
		c.Expect(stopresumerunTimes, gs.Equals, 3)
		c.Expect(len(stopresumeHolder), gs.Equals, 2)
		c.Expect(stopresumeHolder[1], gs.Equals, "woot")
		c.Expect(oRunner.retainPack, gs.IsNil)
	})
}
