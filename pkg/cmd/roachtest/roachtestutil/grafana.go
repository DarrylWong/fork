// Copyright 2024 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package roachtestutil

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachprod/grafana"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/cockroach/pkg/roachprod/vm"
	"github.com/cockroachdb/errors"
)

// AddGrafanaAnnotationPoint is a wrapper over c.AddGrafanaAnnotation that
// should be used by tests to add annotations to the centralized grafana instance.
// It handles creating an annotation request with the appropriate tags, as well
// as error handling to not fail a test for non request formatting issues.
func AddGrafanaAnnotationPoint(
	ctx context.Context, c cluster.Cluster, l *logger.Logger, t test.Test, args ...interface{},
) error {
	req := createAddAnnotationRequest(c, t, args...)
	return addGrafanaAnnotation(ctx, c, l, t, req)
}

// AddGrafanaAnnotationRange is a wrapper over c.AddGrafanaAnnotation that
// should be used by tests to add annotations over a time range to the centralized
// grafana instance. It handles creating an annotation request with the
// appropriate tags, as well as error handling to not fail a test for non
// request formatting issues.
func AddGrafanaAnnotationRange(
	ctx context.Context,
	c cluster.Cluster,
	l *logger.Logger,
	t test.Test,
	start time.Time,
	end time.Time,
	args ...interface{},
) error {
	req := createAddAnnotationRequest(c, t, args...)

	// Use epoch time but only if the time is not zero. Calling
	// Unix() on a time of zero returns the minimum Unix time, but
	// we want it to be 0 if not set.
	if !start.IsZero() {
		req.StartTime = start.UnixMilli()
	}
	if !end.IsZero() {
		req.EndTime = end.UnixMilli()
	}

	return addGrafanaAnnotation(ctx, c, l, t, req)
}

func addGrafanaAnnotation(
	ctx context.Context,
	c cluster.Cluster,
	l *logger.Logger,
	t test.Test,
	req grafana.AddAnnotationRequest,
) error {
	if t.RunID() == "" {
		// Grafana is not available if the RunID is not set. i.e. the test is not running on gce.
		// Return nil instead of returning or logging an error here to avoid spamming the logs.
		return nil
	}
	if req.Text == "" {
		return errors.New("test.AddGrafanaAnnotation: text must be specified")
	}

	// Don't fail the roachtest because it can't add the annotation,
	// log the error and continue.
	res, err := c.AddGrafanaAnnotation(ctx, l, false /* internal */, req)
	if err != nil {
		l.Printf("Adding Grafana annotation failed with: %s", err)
	}
	l.Printf("Grafana annotation successfully added: %s", res)
	return nil
}

func createAddAnnotationRequest(
	c cluster.Cluster, t test.Test, args ...interface{},
) grafana.AddAnnotationRequest {
	text := fmt.Sprint(args...)

	// We add the tags: cluster name, test name, and test run ID
	// The grafana dashboards are filtered by these three template
	// variables, so adding all three allows for easy annotation
	// queries to be made.
	return grafana.AddAnnotationRequest{
		Text: text,
		Tags: []string{c.Name(), vm.SanitizeLabel(t.Name()), t.RunID()},
	}
}
