// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

//go:build !metamorphic_disable
// +build !metamorphic_disable

package metamorphic

import "github.com/cockroachdb/cockroach/pkg/util/envutil"

// disableMetamorphicTesting can be used to disable metamorphic tests. If it
// is set to true then metamorphic testing will not be enabled.
var disableMetamorphicTesting = envutil.EnvOrDefaultBool(DisableMetamorphicEnvVar, false)

var specifiedMetamorphicConstantsFile = envutil.EnvOrDefaultString(SpecifyMetamorphicConstantsEnvVar, "")

var usingSpecifiedMetamorphicConstants = specifiedMetamorphicConstantsFile != ""

var storedMetamorphicConstants map[string]string
