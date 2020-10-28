// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// +build kubelet

package listeners

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/datadog-agent/pkg/config"
	"github.com/DataDog/datadog-agent/pkg/util/containers"
	"github.com/DataDog/datadog-agent/pkg/util/kubernetes/kubelet"
)

func getMockedPods() []*kubelet.Pod {
	containerSpecs := []kubelet.ContainerSpec{
		{
			Name:  "foo",
			Image: "datadoghq.com/foo:latest",
			Ports: []kubelet.ContainerPortSpec{
				// test that resolved ports are sorted in ascending order
				{
					ContainerPort: 1339,
					HostPort:      1340,
					Name:          "fooudpport",
					Protocol:      "UDP",
				},
				{
					ContainerPort: 1337,
					HostPort:      1338,
					Name:          "footcpport",
					Protocol:      "TCP",
				},
			},
		},
		{
			Name:  "bar",
			Image: "datadoghq.com/bar:latest",
			Ports: []kubelet.ContainerPortSpec{
				{
					ContainerPort: 1122,
					HostPort:      1133,
					Name:          "barport",
					Protocol:      "TCP",
				},
			},
		},
		{
			Name:  "baz",
			Image: "datadoghq.com/baz:latest",
			Ports: []kubelet.ContainerPortSpec{
				{
					ContainerPort: 1122,
					HostPort:      1133,
					Name:          "barport",
					Protocol:      "TCP",
				},
			},
		},
		{ // For now, we include default pause containers in the autodiscovery
			Name:  "clustercheck",
			Image: "k8s.gcr.io/pause:latest",
			Ports: []kubelet.ContainerPortSpec{
				{
					ContainerPort: 1122,
					HostPort:      1133,
					Name:          "barport",
					Protocol:      "TCP",
				},
			},
		},
		{
			Name:  "excluded",
			Image: "datadoghq.com/baz:latest",
			Ports: []kubelet.ContainerPortSpec{
				{
					ContainerPort: 1122,
					HostPort:      1133,
					Name:          "barport",
					Protocol:      "TCP",
				},
			},
		},
		{
			Name:  "metrics-excluded",
			Image: "metrics/excluded:latest",
			Ports: []kubelet.ContainerPortSpec{
				{
					ContainerPort: 1122,
					HostPort:      1133,
					Name:          "barport",
					Protocol:      "TCP",
				},
			},
		},
		{
			Name:  "logs-excluded",
			Image: "logs/excluded:latest",
			Ports: []kubelet.ContainerPortSpec{
				{
					ContainerPort: 1122,
					HostPort:      1133,
					Name:          "barport",
					Protocol:      "TCP",
				},
			},
		},
		{
			Name:  "bad-status",
			Image: "datadoghq.com/foo:latest",
			Ports: []kubelet.ContainerPortSpec{
				{
					ContainerPort: 1122,
					HostPort:      1133,
					Name:          "barport",
					Protocol:      "TCP",
				},
			},
		},
	}
	kubeletSpec := kubelet.Spec{
		HostNetwork: false,
		NodeName:    "mockn-node",
		Containers:  containerSpecs,
	}
	containerStatuses := []kubelet.ContainerStatus{
		{
			Name:  "foo",
			Image: "datadoghq.com/foo:latest",
			ID:    "docker://foorandomhash",
		},
		{
			Name:  "bar",
			Image: "datadoghq.com/bar:latest",
			ID:    "rkt://bar-random-hash",
		},
		{
			Name:  "baz",
			Image: "datadoghq.com/baz:latest",
			ID:    "docker://containerid",
		},
		{
			Name:  "clustercheck",
			Image: "k8s.gcr.io/pause:latest",
			ID:    "docker://clustercheck",
		},
		{
			Name:  "excluded",
			Image: "datadoghq.com/baz:latest",
			ID:    "docker://excluded",
		},
		{
			Name:  "metrics-excluded",
			Image: "metrics/excluded:latest",
			ID:    "docker://metrics-excluded",
		},
		{
			Name:  "logs-excluded",
			Image: "logs/excluded:latest",
			ID:    "docker://logs-excluded",
		},
		{
			Name:  "bad-status",
			Image: "datadoghq.com/bar:latest",
			ID:    "docker://bad-status-random-hash",
		},
	}
	kubeletStatus := kubelet.Status{
		Phase:      "Running",
		PodIP:      "127.0.0.1",
		HostIP:     "127.0.0.2",
		Containers: containerStatuses,
	}
	return []*kubelet.Pod{
		{
			Spec:   kubeletSpec,
			Status: kubeletStatus,
			Metadata: kubelet.PodMetadata{
				UID:  "mock-pod-uid",
				Name: "mock-pod",
				Annotations: map[string]string{
					"ad.datadoghq.com/baz.check_names": "[\"baz_check\"]",
					"ad.datadoghq.com/baz.instances":   "[]",
				},
			},
		},
	}
}

func TestProcessNewPod(t *testing.T) {
	config.Datadog.SetDefault("ac_include", []string{"name:baz"})
	config.Datadog.SetDefault("ac_exclude", []string{"image:datadoghq.com/baz.*"})
	config.Datadog.SetDefault("container_exclude_metrics", []string{"name:metrics-excluded"})
	config.Datadog.SetDefault("container_exclude_logs", []string{"name:logs-excluded"})
	config.Datadog.SetDefault("exclude_pause_container", true)

	defer func() {
		config.Datadog.SetDefault("ac_include", []string{})
		config.Datadog.SetDefault("ac_exclude", []string{})
		config.Datadog.SetDefault("container_exclude_metrics", []string{})
		config.Datadog.SetDefault("container_exclude_logs", []string{})
		config.Datadog.SetDefault("exclude_pause_container", true)
	}()

	services := make(chan Service, 8)
	listener := KubeletListener{
		newService: services,
		services:   make(map[string]Service),
	}
	listener.filters, _ = newContainerFilters()

	listener.processNewPods(getMockedPods(), false)

	select {
	case service := <-services:
		assert.Equal(t, "docker://foorandomhash", service.GetEntity())
		assert.Equal(t, "container_id://foorandomhash", service.GetTaggerEntity())
		adIdentifiers, err := service.GetADIdentifiers()
		assert.Nil(t, err)
		assert.Equal(t, []string{"docker://foorandomhash", "datadoghq.com/foo:latest", "foo"}, adIdentifiers)
		hosts, err := service.GetHosts()
		assert.Nil(t, err)
		assert.Equal(t, map[string]string{"pod": "127.0.0.1"}, hosts)
		ports, err := service.GetPorts()
		assert.Nil(t, err)
		assert.Equal(t, []ContainerPort{{1337, "footcpport"}, {1339, "fooudpport"}}, ports)
		_, err = service.GetPid()
		assert.Equal(t, ErrNotSupported, err)
		assert.Len(t, service.GetCheckNames(), 0)
	default:
		assert.FailNow(t, "first service not in channel")
	}

	select {
	case service := <-services:
		assert.Equal(t, "rkt://bar-random-hash", service.GetEntity())
		assert.Equal(t, "container_id://bar-random-hash", service.GetTaggerEntity())
		adIdentifiers, err := service.GetADIdentifiers()
		assert.Nil(t, err)
		assert.Equal(t, []string{"rkt://bar-random-hash", "datadoghq.com/bar:latest", "bar"}, adIdentifiers)
		hosts, err := service.GetHosts()
		assert.Nil(t, err)
		assert.Equal(t, map[string]string{"pod": "127.0.0.1"}, hosts)
		ports, err := service.GetPorts()
		assert.Nil(t, err)
		assert.Equal(t, []ContainerPort{{1122, "barport"}}, ports)
		_, err = service.GetPid()
		assert.Equal(t, ErrNotSupported, err)
		assert.Len(t, service.GetCheckNames(), 0)
		assert.False(t, service.HasFilter(containers.MetricsFilter))
		assert.False(t, service.HasFilter(containers.LogsFilter))
	default:
		assert.FailNow(t, "second service not in channel")
	}

	select {
	case service := <-services:
		assert.Equal(t, "docker://containerid", service.GetEntity())
		assert.Equal(t, "container_id://containerid", service.GetTaggerEntity())
		adIdentifiers, err := service.GetADIdentifiers()
		assert.Nil(t, err)
		assert.Equal(t, []string{"docker://containerid", "datadoghq.com/baz:latest", "baz"}, adIdentifiers)
		hosts, err := service.GetHosts()
		assert.Nil(t, err)
		assert.Equal(t, map[string]string{"pod": "127.0.0.1"}, hosts)
		ports, err := service.GetPorts()
		assert.Nil(t, err)
		assert.Equal(t, []ContainerPort{{1122, "barport"}}, ports)
		_, err = service.GetPid()
		assert.Equal(t, ErrNotSupported, err)
		assert.Equal(t, []string{"baz_check"}, service.GetCheckNames())
		assert.False(t, service.HasFilter(containers.MetricsFilter))
		assert.False(t, service.HasFilter(containers.LogsFilter))
	default:
		assert.FailNow(t, "third service not in channel")
	}

	select {
	case service := <-services:
		assert.Equal(t, "docker://clustercheck", service.GetEntity())
		assert.Equal(t, "container_id://clustercheck", service.GetTaggerEntity())
		adIdentifiers, err := service.GetADIdentifiers()
		assert.Nil(t, err)
		assert.Equal(t, []string{"docker://clustercheck", "k8s.gcr.io/pause:latest", "pause"}, adIdentifiers)
		hosts, err := service.GetHosts()
		assert.Nil(t, err)
		assert.Equal(t, map[string]string{"pod": "127.0.0.1"}, hosts)
		ports, err := service.GetPorts()
		assert.Nil(t, err)
		assert.Equal(t, []ContainerPort{{1122, "barport"}}, ports)
		_, err = service.GetPid()
		assert.Equal(t, ErrNotSupported, err)
		assert.Len(t, service.GetCheckNames(), 0)
		assert.False(t, service.HasFilter(containers.MetricsFilter))
		assert.False(t, service.HasFilter(containers.LogsFilter))
	default:
		assert.FailNow(t, "fourth service not in channel")
	}

	// Fifth container is filtered out
	// Sixth and seventh containers are metrics and logs filtered

	select {
	case service := <-services:
		assert.Equal(t, "docker://metrics-excluded", service.GetEntity())
		assert.Equal(t, "container_id://metrics-excluded", service.GetTaggerEntity())
		adIdentifiers, err := service.GetADIdentifiers()
		assert.Nil(t, err)
		assert.Equal(t, []string{"docker://metrics-excluded", "metrics/excluded:latest", "excluded"}, adIdentifiers)
		hosts, err := service.GetHosts()
		assert.Nil(t, err)
		assert.Equal(t, map[string]string{"pod": "127.0.0.1"}, hosts)
		ports, err := service.GetPorts()
		assert.Nil(t, err)
		assert.Equal(t, []ContainerPort{{1122, "barport"}}, ports)
		_, err = service.GetPid()
		assert.Equal(t, ErrNotSupported, err)
		assert.Len(t, service.GetCheckNames(), 0)
		assert.True(t, service.HasFilter(containers.MetricsFilter))
		assert.False(t, service.HasFilter(containers.LogsFilter))
	default:
		assert.FailNow(t, "fifth service not in channel")
	}

	select {
	case service := <-services:
		assert.Equal(t, "docker://logs-excluded", service.GetEntity())
		assert.Equal(t, "container_id://logs-excluded", service.GetTaggerEntity())
		adIdentifiers, err := service.GetADIdentifiers()
		assert.Nil(t, err)
		assert.Equal(t, []string{"docker://logs-excluded", "logs/excluded:latest", "excluded"}, adIdentifiers)
		hosts, err := service.GetHosts()
		assert.Nil(t, err)
		assert.Equal(t, map[string]string{"pod": "127.0.0.1"}, hosts)
		ports, err := service.GetPorts()
		assert.Nil(t, err)
		assert.Equal(t, []ContainerPort{{1122, "barport"}}, ports)
		_, err = service.GetPid()
		assert.Equal(t, ErrNotSupported, err)
		assert.Len(t, service.GetCheckNames(), 0)
		assert.False(t, service.HasFilter(containers.MetricsFilter))
		assert.True(t, service.HasFilter(containers.LogsFilter))
	default:
		assert.FailNow(t, "sixth service not in channel")
	}

	// eighth container has a different image name in spec and status
	select {
	case service := <-services:
		assert.Equal(t, "docker://bad-status-random-hash", service.GetEntity())
		assert.Equal(t, "container_id://bad-status-random-hash", service.GetTaggerEntity())
		adIdentifiers, err := service.GetADIdentifiers()
		assert.Nil(t, err)
		assert.Equal(t, []string{"docker://bad-status-random-hash", "datadoghq.com/foo:latest", "foo"}, adIdentifiers)
		hosts, err := service.GetHosts()
		assert.Nil(t, err)
		assert.Equal(t, map[string]string{"pod": "127.0.0.1"}, hosts)
		ports, err := service.GetPorts()
		assert.Nil(t, err)
		assert.Equal(t, []ContainerPort{{1122, "barport"}}, ports)
		_, err = service.GetPid()
		assert.Equal(t, ErrNotSupported, err)
		assert.Len(t, service.GetCheckNames(), 0)
		assert.False(t, service.HasFilter(containers.MetricsFilter))
		assert.False(t, service.HasFilter(containers.LogsFilter))
	default:
		assert.FailNow(t, "eighth service not in channel")
	}

	// Pod service
	select {
	case service := <-services:
		assert.Equal(t, "kubernetes_pod://mock-pod-uid", service.GetEntity())
		assert.Equal(t, "kubernetes_pod_uid://mock-pod-uid", service.GetTaggerEntity())
		adIdentifiers, err := service.GetADIdentifiers()
		assert.Nil(t, err)
		assert.Equal(t, []string{"kubernetes_pod://mock-pod-uid"}, adIdentifiers)
		hosts, err := service.GetHosts()
		assert.Nil(t, err)
		assert.Equal(t, map[string]string{"pod": "127.0.0.1"}, hosts)
		ports, err := service.GetPorts()
		assert.Nil(t, err)
		assert.Equal(t, []ContainerPort{{1122, "barport"}, {1122, "barport"}, {1122, "barport"}, {1122, "barport"}, {1122, "barport"}, {1122, "barport"}, {1122, "barport"}, {1337, "footcpport"}, {1339, "fooudpport"}}, ports)
		_, err = service.GetPid()
		assert.Equal(t, ErrNotSupported, err)
		assert.Len(t, service.GetCheckNames(), 0)
		assert.False(t, service.HasFilter(containers.MetricsFilter))
		assert.False(t, service.HasFilter(containers.LogsFilter))
	default:
		assert.FailNow(t, "pod service not in channel")
	}

	select {
	case <-services:
		assert.FailNow(t, "9 services in channel, filtering is broken")
	default:
		// all good
	}
}