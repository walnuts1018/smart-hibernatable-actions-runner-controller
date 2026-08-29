//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/test/utils"
)

// namespace where the project is deployed in
const namespace = "smart-hibernatable-actions-runner-controller-system"
const testNamespace = "sharc-e2e-test"

const dummyRSAPrivateKeyPEM = `-----BEGIN PRIVATE KEY-----
MIIEvwIBADANBgkqhkiG9w0BAQEFAASCBKkwggSlAgEAAoIBAQC11udPh2QLYPFH
QOvdv7G6yxfheEbBTcj2FsgfqaJGpBb8TlEe0nD3GHe4TJKxdOUbDtOQpbBqFIpX
PVrYUuvFEWlvSvabwWrYbbTnW7yg+YO5HUf4rMmyGt8ZtPvvuymZzBOPk+yfx6Ld
iBIUEuqa1bxk7gahcx+zlxfPqg471Q7zo0BqUy1IgBP2QqPnWZFlP94fg3xbbrGa
LZj1OiAVIZmQu2QaTP75vye/XrTGDD5UtQRg/d/LcAv7iddP68KbT3o0xlryjjzp
n15YqAib0YvWosAde+Z/O2pV6gpn6pvq2trDuC09++SysgWRA9g0IWLWY7X5R1/H
AZDxyaNBAgMBAAECggEAO0yFyk2gtoU6qb3mLT5iO0QX2ZNbn5Y6PuZXBNxQ6zB/
vm/bzG1cIXh9MkDmZbB1NkmzfKxLx4xDQQflJD6GXJG9DGop2clNip7cK8ai0OwN
pMSDv/i5HbfdoYh/0EH84wbGKkBXHhQAbLX/D0TL9QpWkaN9zhC4+dwAC9ytH51i
axShF6CHgydYxNZu/bf5XuNXXlY80LJ+Ud0rd1lXKFbz5fYxgs43RkFg9CWWqqDC
rRweNs8evqzATOrD7REccX/QTUJjgXvRaGlVfiyJQHAUyD7vbY0IqgdWJRlTC3r9
MwJri5QjK+UVdxWGcMdvO++su0mhrpSNnM6II4+68QKBgQDmJrvVtuCzbtAjlvGO
jHQhwOYlysH3gcccwS/Y6RV0Y61DZm7v2s0kp85wtO73iuHnC2KwwXjgi42R0VFr
gAWWkEJ8MddX9LwL62+/bnv2A/ajVWIdY1wUovHRdTx3W3qJWZ+684aC+nFRsgtz
miBBsyfNPmGz2ZLYMC3mbS82FwKBgQDKQx384YUsI5VAk6W/fiw2+W+KmhEpKD2P
t3VHD3zNBUb1wMTReZ8RORDHQ9qZiIEMU+zCRHfyMhd7PzexPYMRzavBPqYsrW5O
Q93n8LZC1H9InWGCC2Elk3WgliGqkHZLCHGF5GrK5G49w1V7m/ms+4zD/HQT8evd
9dY9+DggZwKBgQDYXhnAdUkR51+t1b4KMWkMQnkbll57/XnfQo9k8NvGq967upUY
0S6DA29E7hSqi9qMh1ukqH6nOwtAxvQwiA642a5na8PzYJVY72IDKi9HvbolG6Q9
1KdAj1+fdwP9gfbVIXjVHRScFi5qi2PQrlkc6vzEK51Wo3k13TWJp6P2yQKBgQCo
eWF4K21fB8ChipqMOA+iNwEG5TAYJTGqDTk92JOuvo+N0mTeyzyI/wyPvmBOdNpx
J1LVumxirADNIypDkyYi5TsEeye1nTx9KqCjOujGH/RpytXWmZ3wy7Q17/fY9/3g
oAbXbRzbJY0CGzuP+6rrwJhPA3C40FEUkFpFQgWWTwKBgQCyqjpc74Kvc0TFrZ3d
uOIW8u0YbaViHk0OV+qO1HjUXDFAESwOGJ8nu92Rf2rui4Wbw4JD4s4s9kofQh/e
YbdWk16w6qaQF2QKr0hUHNyFy5027uabeQulxMRBxEt5kfoS9ul9CqSePkoH99WB
83Az7XN4r9nFbKH6NdKHUw6GOQ==
-----END PRIVATE KEY-----`

var _ = Describe("SHARC Operator E2E Suite", Ordered, func() {
	var controllerPodName string

	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("creating test namespace")
		cmd = exec.Command("kubectl", "create", "ns", testNamespace)
		_, _ = utils.Run(cmd)

		By("installing CRDs")
		cmd = exec.Command("mise", "run", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("mise", "run", "deploy")
		cmd.Env = append(os.Environ(), fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	AfterAll(func() {
		By("cleaning up test namespace")
		cmd := exec.Command("kubectl", "delete", "ns", testNamespace, "--ignore-not-found=true")
		_ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("mise", "run", "undeploy")
		_ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("mise", "run", "uninstall")
		_ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found=true")
		_ = utils.Run(cmd)
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Scenario 1: Manager & Webhook Health", func() {
		It("should run controller-manager pod successfully", func() {
			verifyControllerUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1))
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller"))

				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"))
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the controller pod is ready", func() {
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())
		})

		It("should reject invalid resources via admission webhook validation", func() {
			By("attempting to create a RunnerMachine with invalid Redfish endpoint protocol")
			invalidMachineYAML := fmt.Sprintf(`
apiVersion: sharc.walnuts.dev/v1alpha1
kind: RunnerMachine
metadata:
  name: invalid-machine
  namespace: %s
spec:
  clusterRef:
    name: test-cluster
  nodeName: invalid-node
  redfish:
    endpoint: "ftp://192.168.1.100"
    credentialsSecretRef:
      name: dummy-secret
`, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(invalidMachineYAML)
			_, err := utils.Run(cmd)
			Expect(err).To(HaveOccurred(), "Expected admission webhook to reject ftp:// protocol endpoint")
		})
	})

	Context("Scenario 2: RunnerCluster, RunnerNodePool & RunnerMachine Lifecycle", func() {
		It("should reconcile RunnerCluster, RunnerNodePool, and RunnerMachine resources", func() {
			By("creating dummy kubeconfig secret for RunnerCluster")
			dummyKubeconfig := `apiVersion: v1
clusters:
- cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
kind: Config
preferences: {}
users:
- name: test-user
  user:
    token: dummy-token
`
			secretYAML := fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: cluster-kubeconfig
  namespace: %s
type: Opaque
stringData:
  kubeconfig: |
%s
---
apiVersion: v1
kind: Secret
metadata:
  name: redfish-creds
  namespace: %s
type: Opaque
stringData:
  username: "admin"
  password: "password"
`, testNamespace, indent(dummyKubeconfig, 4), testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(secretYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create secrets")

			By("creating RunnerCluster, RunnerNodePool, and RunnerMachine")
			resourcesYAML := fmt.Sprintf(`
apiVersion: sharc.walnuts.dev/v1alpha1
kind: RunnerCluster
metadata:
  name: e2e-cluster
  namespace: %s
spec:
  kubeconfigSecretRef:
    name: cluster-kubeconfig
    key: kubeconfig
  runnerNamespace: gha-runners
---
apiVersion: sharc.walnuts.dev/v1alpha1
kind: RunnerNodePool
metadata:
  name: e2e-pool
  namespace: %s
spec:
  clusterRef:
    name: e2e-cluster
  scaling:
    minNodes: 0
    maxNodes: 2
---
apiVersion: sharc.walnuts.dev/v1alpha1
kind: RunnerMachine
metadata:
  name: e2e-machine-01
  namespace: %s
spec:
  clusterRef:
    name: e2e-cluster
  nodePoolRef:
    name: e2e-pool
  nodeName: kind-worker
  powerPolicy: AlwaysOn
  redfish:
    endpoint: "https://127.0.0.1:8443"
    systemID: "1"
    credentialsSecretRef:
      name: redfish-creds
    tls:
      insecureSkipVerify: true
`, testNamespace, testNamespace, testNamespace)

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(resourcesYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create CRD resources")

			By("verifying RunnerCluster status generation is observed")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "runnercluster", "e2e-cluster",
					"-n", testNamespace, "-o", "jsonpath={.status.observedGeneration}")
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).NotTo(BeEmpty())
			}, 1*time.Minute, 2*time.Second).Should(Succeed())

			By("verifying RunnerNodePool status is populated")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "runnernodepool", "e2e-pool",
					"-n", testNamespace, "-o", "jsonpath={.status.observedGeneration}")
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).NotTo(BeEmpty())
			}, 1*time.Minute, 2*time.Second).Should(Succeed())
		})
	})

	Context("Scenario 3: RunnerScaleSet & EphemeralRunnerSet Lifecycle", func() {
		It("should reconcile RunnerScaleSet and automatically manage Listener deployment", func() {
			By("creating GitHub credentials secret")
			ghSecretYAML := fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: gha-app-secret
  namespace: %s
type: Opaque
stringData:
  github_app_id: "123456"
  github_app_installation_id: "789012"
  github_app_private_key: |
%s
`, testNamespace, indent(dummyRSAPrivateKeyPEM, 4))

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(ghSecretYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create GitHub app secret")

			By("creating RunnerScaleSet")
			two := 2
			scaleSetYAML := fmt.Sprintf(`
apiVersion: sharc.walnuts.dev/v1alpha1
kind: RunnerScaleSet
metadata:
  name: e2e-scaleset
  namespace: %s
spec:
  github:
    configURL: "https://github.com/walnuts1018/sharc"
    scaleSetName: "e2e-scaleset"
    credentialsSecretRef:
      name: gha-app-secret
  nodePoolRef:
    name: e2e-pool
  scaling:
    minRunners: 0
    maxRunners: %d
  runner:
    template:
      spec:
        restartPolicy: Never
        containers:
        - name: runner
          image: ghcr.io/actions/actions-runner:latest
`, testNamespace, two)

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(scaleSetYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create RunnerScaleSet")

			By("verifying that EphemeralRunnerSet is created for the ScaleSet")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "ephemeralrunnerset", "e2e-scaleset",
					"-n", testNamespace, "-o", "jsonpath={.spec.scaleSetRef.name}")
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("e2e-scaleset"))
			}, 1*time.Minute, 2*time.Second).Should(Succeed())
		})
	})
})

func indent(s string, spaces int) string {
	lines := strings.Split(s, "\n")
	prefix := strings.Repeat(" ", spaces)
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

func encodeBase64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
