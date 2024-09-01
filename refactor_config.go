package mock

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"go.uber.org/atomic"

	networking "istio.io/api/networking/v1alpha3"
	authz "istio.io/api/security/v1beta1"
	api "istio.io/api/type/v1beta1"
	"istio.io/istio/pilot/pkg/model"
	config2 "istio.io/istio/pkg/config"
	"istio.io/istio/pkg/config/schema/collections"
	"istio.io/istio/pkg/config/schema/resource"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/test/config"
	"istio.io/istio/pkg/test/util/retry"
)

var (
	ExampleVirtualService = &networking.VirtualService{
		Hosts: []string{"prod", "test"},
		Http: []*networking.HTTPRoute{
			{
				Route: []*networking.HTTPRouteDestination{
					{
						Destination: &networking.Destination{
							Host: "job",
						},
						Weight: 80,
					},
				},
			},
		},
	}

	ExampleServiceEntry = &networking.ServiceEntry{
		Hosts:      []string{"*.google.com"},
		Resolution: networking.ServiceEntry_NONE,
		Ports: []*networking.ServicePort{
			{Number: 80, Name: "http-name", Protocol: "http"},
			{Number: 8080, Name: "http2-name", Protocol: "http2"},
		},
	}

	ExampleGateway = &networking.Gateway{
		Servers: []*networking.Server{
			{
				Hosts: []string{"google.com"},
				Port:  &networking.Port{Name: "http", Protocol: "http", Number: 10080},
			},
		},
	}

	ExampleDestinationRule = &networking.DestinationRule{
		Host: "ratings",
		TrafficPolicy: &networking.TrafficPolicy{
			LoadBalancer: &networking.LoadBalancerSettings{
				LbPolicy: new(networking.LoadBalancerSettings_Simple),
			},
		},
	}

	ExampleAuthorizationPolicy = &authz.AuthorizationPolicy{
		Selector: &api.WorkloadSelector{
			MatchLabels: map[string]string{
				"app":     "httpbin",
				"version": "v1",
			},
		},
	}

	mockGvk = collections.Mock.GroupVersionKind()
)

// Make creates a mock config indexed by a number
func Make(namespace string, i int) config2.Config {
	name := fmt.Sprintf("%s%d", "mock-config", i)
	return config2.Config{
		Meta: config2.Meta{
			GroupVersionKind: mockGvk,
			Name:             name,
			Namespace:        namespace,
			Labels: map[string]string{
				"key": name,
			},
			Annotations: map[string]string{
				"annotationkey": name,
			},
		},
		Spec: &config.MockConfig{
			Key: name,
			Pairs: []*config.ConfigPair{
				{Key: "key", Value: strconv.Itoa(i)},
			},
		},
	}
}

// Compare checks two configs ignoring revisions and creation time
func Compare(a, b config2.Config) bool {
	a.ResourceVersion = ""
	b.ResourceVersion = ""
	a.CreationTimestamp = time.Time{}
	b.CreationTimestamp = time.Time{}
	return reflect.DeepEqual(a, b)
}

// CheckMapInvariant validates operational invariants of an empty config registry
func CheckMapInvariant(r model.ConfigStore, t *testing.T, namespace string, n int) {
	verifyConfigTypes(r, t)
	elts := createConfigs(namespace, n)
	createConfigsInStore(r, t, elts)
	verifyConfigsInStore(r, t, elts)
	verifyErrorsOnInvalidOperations(r, t, elts)
	deleteConfigsFromStore(r, t, namespace, n)
}

func verifyConfigTypes(r model.ConfigStore, t *testing.T) {
	if _, contains := r.Schemas().FindByGroupVersionKind(mockGvk); !contains {
		t.Fatal("expected config mock types")
	}
	log.Info("Created mock descriptor")
}

func createConfigs(namespace string, n int) map[int]config2.Config {
	elts := make(map[int]config2.Config, n)
	for i := 0; i < n; i++ {
		elts[i] = Make(namespace, i)
	}
	log.Info("Make mock objects")
	return elts
}

func createConfigsInStore(r model.ConfigStore, t *testing.T, elts map[int]config2.Config) {
	for _, elt := range elts {
		if _, err := r.Create(elt); err != nil {
			t.Error(err)
		}
	}
	log.Info("Created mock objects")
}

func verifyConfigsInStore(r model.ConfigStore, t *testing.T, elts map[int]config2.Config) {
	revs := make(map[int]string, len(elts))
	for i, elt := range elts {
		v1 := r.Get(mockGvk, elt.Name, elt.Namespace)
		if v1 == nil || !Compare(elt, *v1) {
			t.Errorf("wanted %v, got %v", elt, v1)
		} else {
			revs[i] = v1.ResourceVersion
		}
	}
	log.Info("Got stored elements")
}

func verifyErrorsOnInvalidOperations(r model.ConfigStore, t *testing.T, elts map[int]config2.Config) {
	invalid := config2.Config{
		Meta: config2.Meta{
			GroupVersionKind: mockGvk,
			Name:             "invalid",
			ResourceVersion:  getFirstRevision(elts),
		},
		Spec: &config.MockConfig{},
	}

	missing := config2.Config{
		Meta: config2.Meta{
			GroupVersionKind: mockGvk,
			Name:             "missing",
			ResourceVersion:  getFirstRevision(elts),
		},
		Spec: &config.MockConfig{Key: "missing"},
	}

	assertError(t, r, config2.Config{})
	assertError(t, r, invalid)
	assertError(t, r, missing)

	verifyMissingElements(r, t)
}

func getFirstRevision(elts map[int]config2.Config) string {
	for _, elt := range elts {
		return elt.ResourceVersion
	}
	return ""
}

func assertError(t *testing.T, r model.ConfigStore, cfg config2.Config) {
	if _, err := r.Create(cfg); err == nil {
		t.Error("expected error posting invalid object")
	}
	if _, err := r.Update(cfg); err == nil {
		t.Error("expected error updating invalid object")
	}
}

func verifyMissingElements(r model.ConfigStore, t *testing.T) {
	if l := r.List(mockGvk, ""); len(l) > 0 {
		t.Errorf("unexpected objects for missing type")
	}
	if cfg := r.Get(mockGvk, "missing", ""); cfg != nil {
		t.Error("unexpected configuration object found")
	}
}

func deleteConfigsFromStore(r model.ConfigStore, t *testing.T, namespace string, n int) {
	for i := 0; i < n; i++ {
		if err := r.Delete(mockGvk, Make(namespace, i).Name, namespace, nil); err != nil {
			t.Error(err)
		}
	}
	log.Info("Delete elements")

	l := r.List(mockGvk, namespace)
	if len(l) != 0 {
		t.Errorf("wanted 0 element(s), got %d in %v", len(l), l)
	}
	log.Info("Test done, deleting namespace")
}

// CheckIstioConfigTypes validates that an empty store can ingest Istio config objects
func CheckIstioConfigTypes(store model.ConfigStore, namespace string, t *testing.T) {
	configName := "example"

	cases := []struct {
		name       string
		configName string
		schema     resource.Schema
		spec       config2.Spec
	}{
		{"VirtualService", configName, collections.VirtualService, ExampleVirtualService},
		{"DestinationRule", configName, collections.DestinationRule, ExampleDestinationRule},
		{"ServiceEntry", configName, collections.ServiceEntry, ExampleServiceEntry},
		{"Gateway", configName, collections.Gateway, ExampleGateway},
		{"AuthorizationPolicy", configName, collections.AuthorizationPolicy, ExampleAuthorizationPolicy},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			configMeta := config2.Meta{
				GroupVersionKind: c.schema.GroupVersionKind(),
				Name:             c.configName,
			}
			if !c.schema.IsClusterScoped() {
				configMeta.Namespace = namespace
			}

			cfg := config2.Config{
				Meta: configMeta,
				Spec: c.spec,
			}

			if _, err := store.Create(cfg); err != nil {
				t.Error(err)
			}

			configs := store.List(c.schema.GroupVersionKind(), namespace)
			if len(configs) == 0 {
				t.Error("expected non-zero number of configs")
			}

			storedConfig := store.Get(c.schema.GroupVersionKind(), c.configName, namespace)
			if storedConfig == nil {
				t.Error("expected to find stored config")
			}
		})
	}
}
