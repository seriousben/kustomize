// Copyright 2019 The Kubernetes Authors.
// SPDX-License-Identifier: Apache-2.0

package target_test

import (
	"strings"
	"testing"

	"sigs.k8s.io/kustomize/v3/pkg/kusttest"
)

const httpsService = `
apiVersion: v1
kind: Service
metadata:
  name: my-https-svc
spec:
  ports:
  - port: 443
    protocol: TCP
    name: https
  selector:
    app: my-app
`

func writeStatefulSetBase(th *kusttest_test.KustTestHarness) {
	th.WriteK("/app/base", `
resources:
- statefulset.yaml
`)
	th.WriteF("/app/base/statefulset.yaml", `
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: my-sts
spec:
  serviceName: my-svc
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
      - name: app
        image: my-image
  volumeClaimTemplates:
  - spec:
      storageClassName: default
`)
}

func writeHTTPSOverlay(th *kusttest_test.KustTestHarness) {
	th.WriteK("/app/https", `
resources:
- ../base
- https-svc.yaml
patchesStrategicMerge:
- sts-patch.yaml
`)
	th.WriteF("/app/https/https-svc.yaml", httpsService)
	th.WriteF("/app/https/sts-patch.yaml", `
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: my-sts
spec:
  serviceName: my-https-svc
`)
}

func writeConfigOverlay(th *kusttest_test.KustTestHarness) {
	th.WriteK("/app/config", `
resources:
- ../base
configMapGenerator:
- name: my-config
  literals:
  - MY_ENV=foo
generatorOptions:
  disableNameSuffixHash: true
patchesStrategicMerge:
- sts-patch.yaml
`)
	th.WriteF("/app/config/sts-patch.yaml", `
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: my-sts
spec:
  template:
    spec:
      containers:
      - name: app
        envFrom:
        - configMapRef:
            name: my-config
`)
}

func writeTolerationsOverlay(th *kusttest_test.KustTestHarness) {
	th.WriteK("/app/tolerations", `
resources:
- ../base
patchesStrategicMerge:
- sts-patch.yaml
`)
	th.WriteF("/app/tolerations/sts-patch.yaml", `
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: my-sts
spec:
  template:
    spec:
      tolerations:
      - effect: NoExecute
        key: node.kubernetes.io/not-ready
        tolerationSeconds: 30
`)
}

func writeStorageOverlay(th *kusttest_test.KustTestHarness) {
	th.WriteK("/app/storage", `
resources:
- ../base
patchesJson6902:
- target:
    group: apps
    version: v1
    kind: StatefulSet
    name: my-sts
  path: sts-patch.json
`)
	th.WriteF("/app/storage/sts-patch.json", `
[{"op": "replace", "path": "/spec/volumeClaimTemplates/0/spec/storageClassName", "value": "my-sc"}]
`)
}

func writePatchConfig(th *kusttest_test.KustTestHarness) {
	writeStorageOverlay(th)
	writeConfigOverlay(th)
	writeTolerationsOverlay(th)
	writeHTTPSOverlay(th)
}

// Here's a complex kustomization scenario that combines multiple overlays
// on a common base:
//
//                 dev                       prod
//                  |                         |
//                  |                         |
//        + ------- +          + ------------ + ------------- +
//        |         |          |              |               |
//        |         |          |              |               |
//        v         |          v              v               v
//     storage      + -----> config       tolerations       https
//        |                    |              |               |
//        |                    |              |               |
//        |                    + --- +  + --- +               |
//        |                          |  |                     |
//        |                          v  v                     |
//        + -----------------------> base <------------------ +
//
// The base resource is a statefulset. Each intermediate overlay manages or
// generates new resources and patches different aspects of the same base
// resource, without using any of the `namePrefix`, `nameSuffix` or `namespace`
// kustomization keywords.
//
// Intermediate overlays:
//   - storage: Changes the storage class of the stateful set with a JSON patch.
//   - config: Generates a config map and adds a field as an environment
//             variable.
//   - tolerations: Adds a new tolerations field in the spec.
//   - https: Adds a new service resource and changes the service name in the
//            stateful set.
//
// Top overlays:
//   - dev: Combines the storage and config intermediate overlays.
//   - prod: Combines the config, tolerations and https intermediate overlays.

func TestComplexComposition_Dev_Failure(t *testing.T) {
	th := kusttest_test.NewKustTestHarness(t, "/app/dev")
	writeStatefulSetBase(th)
	writePatchConfig(th)
	th.WriteK("/app/dev", `
resources:
- ../storage
- ../config
`)

	_, err := th.MakeKustTarget().MakeCustomizedResMap()
	if err == nil {
		t.Fatalf("Expected resource accumulation error")
	}
	if !strings.Contains(
		err.Error(), "already registered id: apps_v1_StatefulSet|~X|my-sts") {
		t.Fatalf("Unexpected err: %v", err)
	}

	// Expected Output
	const devMergeResult = `
apiVersion: v1
data:
  MY_ENV: foo
kind: ConfigMap
metadata:
  name: my-config
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: my-sts
spec:
  serviceName: my-svc
  selector:
    matchLabels:
    app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
      - name: app
        image: my-image
        envFrom:
        - configMapRef:
            name: my-config
  volumeClaimTemplates:
  - spec:
      storageClassName: my-sc
  `
}

func TestComplexComposition_Prod_Failure(t *testing.T) {
	th := kusttest_test.NewKustTestHarness(t, "/app/prod")
	writeStatefulSetBase(th)
	writePatchConfig(th)

	th.WriteK("/app/prod", `
resources:
- ../config
- ../tolerations
- ../https
`)

	_, err := th.MakeKustTarget().MakeCustomizedResMap()
	if err == nil {
		t.Fatalf("Expected resource accumulation error")
	}
	if !strings.Contains(
		err.Error(), "already registered id: apps_v1_StatefulSet|~X|my-sts") {
		t.Fatalf("Unexpected err: %v", err)
	}

	// Expected Output
	const prodMergeResult = `
apiVersion: v1
data:
  MY_ENV: foo
kind: ConfigMap
metadata:
  name: my-config
---
apiVersion: v1
kind: Service
metadata:
  name: my-https-svc
spec:
  ports:
  - name: https
    port: 443
    protocol: TCP
  selector:
    app: my-app
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: my-sts
spec:
  serviceName: my-https-svc
  selector:
    matchLabels:
    app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
      - image: my-image
        envFrom:
        - configMapRef:
            name: my-config
        name: app
      tolerations:
      - effect: NoExecute
        key: node.kubernetes.io/not-ready
        tolerationSeconds: 30
  volumeClaimTemplates:
  - spec:
      storageClassName: default
  `
}
