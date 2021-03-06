// Copyright (c) 2020 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package time

import (
	"time"
)

// Time is our shim interface to the time package.
type Time interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	Until(t time.Time) time.Duration
	After(t time.Duration) <-chan time.Time
}

func NewRealTime() Time {
	return &realTime{}
}

// realTime is the real implementation of timeIface, which calls through to the real time package.
type realTime struct{}

func (realTime) Until(t time.Time) time.Duration {
	return time.Until(t)
}

func (realTime) After(t time.Duration) <-chan time.Time {
	return time.After(t)
}

func (realTime) Now() time.Time {
	return time.Now()
}

func (realTime) Since(t time.Time) time.Duration {
	return time.Since(t)
}
