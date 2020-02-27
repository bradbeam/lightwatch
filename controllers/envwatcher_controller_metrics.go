package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// downloadTimeSecs tracks the duration of the download step
	downloadTimeSecs = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "lightwatch_download_duration_seconds",
			Help:       "how long it took to download file",
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		},
		[]string{"resource"},
	)

	// resourceCount tracks the number of CRDs that are being watched
	resourceCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "lightwatch_resource_total",
			Help: "Number of resources controller is aware of",
		},
	)

	// resourceUpdates tracks the number of updates to the configmap for a given resource
	resourceUpdates = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lightwatch_resource_updates_total",
			Help: "Number of updates for specified resource",
		},
		[]string{"resource"},
	)
)

func init() {

	metrics.Registry.MustRegister(downloadTimeSecs)
	metrics.Registry.MustRegister(resourceCount)
	metrics.Registry.MustRegister(resourceUpdates)

}
