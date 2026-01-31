package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/hcloud"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	defaultTimeout   = 5 * time.Minute
	pollInterval     = 5 * time.Second
	logInterval      = 30 * time.Second
	nodeReadyTimeout = 5 * time.Minute
)

type e2eConfig struct {
	kubeconfig      string
	hcloudEndpoint  string
	selector        string
	labelKey        string
	setupAnnotation string
	namespace       string
	fipCount        int
	timeout         time.Duration
}

func TestFloatingIPAssignment(t *testing.T) {
	if os.Getenv("E2E_RUN") != "1" {
		t.Skip("E2E_RUN=1 is required to run e2e tests")
	}

	cfg := loadConfig(t)

	ctx := context.Background()
	kubeClient := newKubeClient(t, cfg.kubeconfig)
	token := secretToken(ctx, t, kubeClient, cfg.namespace)
	if token == "" {
		t.Fatalf("secret %s/hcloud must exist with a token", cfg.namespace)
	}
	hcloudClient := newHcloudClient(token, cfg.hcloudEndpoint)

	resetNodeState(ctx, t, kubeClient, cfg.labelKey, cfg.setupAnnotation)
	readyCtx, readyCancel := context.WithTimeout(ctx, nodeReadyTimeout)
	nodes := waitForReadyNodes(readyCtx, t, kubeClient, 2)
	readyCancel()
	if cfg.fipCount > len(nodes) {
		t.Fatalf("E2E_FIP_COUNT=%d exceeds ready nodes=%d", cfg.fipCount, len(nodes))
	}
	validateNodes(t, nodes, cfg.labelKey)
	if !tokenMatchesCluster(ctx, t, hcloudClient, nodes) {
		t.Fatalf("token from secret %s/hcloud cannot access cluster servers", cfg.namespace)
	}
	serverID := nodeServerID(t, nodes[0])
	location := nodeLocation(ctx, t, hcloudClient, serverID)
	if location == nil {
		location = locationFromNodeLabels(ctx, t, hcloudClient, nodes)
	}
	if location == nil {
		t.Fatal("unable to determine hcloud location for floating IPs")
	}

	labels := parseSelector(t, cfg.selector)
	fips, created := ensureFloatingIPs(ctx, t, hcloudClient, cfg.selector, labels, location, 0, cfg.fipCount)
	resetFloatingIPs(ctx, t, hcloudClient, fips)
	t.Cleanup(func() {
		deleteFloatingIPs(context.Background(), t, hcloudClient, created)
	})

	assignments := make(map[int]string, len(fips))
	for _, fip := range fips {
		assignCtx, assignCancel := context.WithTimeout(ctx, cfg.timeout)
		nodeName := waitForAssignment(assignCtx, t, kubeClient, hcloudClient, fip.ID, cfg.labelKey, "")
		assignCancel()

		eventCtx, eventCancel := context.WithTimeout(ctx, cfg.timeout)
		waitForEvent(eventCtx, t, kubeClient, cfg.namespace, "FloatingIPAssigned", fmt.Sprintf("%s assigned to Node", fip.IP.String()))
		eventCancel()
		assignments[fip.ID] = nodeName
	}

	clearStaleLabels(ctx, t, kubeClient, cfg.labelKey, fips)
	if !hasLabelFreeNode(kubeClient, cfg.labelKey) {
		t.Fatalf("no schedulable node without %s label; set E2E_FIP_COUNT=1 or add another node to allow reassignment", cfg.labelKey)
	}

	firstFIP := fips[0]
	originalNode := assignments[firstFIP.ID]
	t.Cleanup(func() {
		_ = setNodeUnschedulable(context.Background(), kubeClient, originalNode, false)
	})

	if err := setNodeUnschedulable(ctx, kubeClient, originalNode, true); err != nil {
		t.Fatalf("failed to cordon node %s: %v", originalNode, err)
	}

	moveCtx, moveCancel := context.WithTimeout(ctx, cfg.timeout)
	movedNode := waitForAssignment(moveCtx, t, kubeClient, hcloudClient, firstFIP.ID, cfg.labelKey, originalNode)
	moveCancel()
	if movedNode == originalNode {
		t.Fatalf("expected floating IP %s to move off cordoned node %s", firstFIP.IP.String(), originalNode)
	}
}

func loadConfig(t *testing.T) e2eConfig {
	t.Helper()
	kubeconfig := requiredEnv(t, "KUBECONFIG")
	selector := requiredEnv(t, "E2E_SELECTOR")
	labelKey := requiredEnv(t, "E2E_LABEL_KEY")
	setupAnnotation := strings.TrimSpace(os.Getenv("E2E_SETUP_ANNOTATION"))
	namespace := requiredEnv(t, "E2E_NAMESPACE")
	timeout := defaultTimeout
	if raw := os.Getenv("E2E_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("invalid E2E_TIMEOUT %q: %v", raw, err)
		}
		timeout = parsed
	}
	fipCount := 1
	if raw := os.Getenv("E2E_FIP_COUNT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("invalid E2E_FIP_COUNT %q: %v", raw, err)
		}
		fipCount = parsed
	}

	return e2eConfig{
		kubeconfig:      kubeconfig,
		hcloudEndpoint:  os.Getenv("HCLOUD_ENDPOINT"),
		selector:        selector,
		labelKey:        labelKey,
		setupAnnotation: setupAnnotation,
		namespace:       namespace,
		fipCount:        fipCount,
		timeout:         timeout,
	}
}

func requiredEnv(t *testing.T, key string) string {
	t.Helper()
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		t.Fatalf("%s must be set", key)
	}
	return val
}

func newKubeClient(t *testing.T, kubeconfig string) *kubernetes.Clientset {
	t.Helper()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("failed to load kubeconfig %s: %v", kubeconfig, err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("failed to create kube client: %v", err)
	}
	return client
}

func newHcloudClient(token, endpoint string) *hcloud.Client {
	opts := []hcloud.ClientOption{
		hcloud.WithToken(token),
	}
	if endpoint != "" {
		opts = append(opts, hcloud.WithEndpoint(endpoint))
	}
	return hcloud.NewClient(opts...)
}

func waitForReadyNodes(ctx context.Context, t *testing.T, client *kubernetes.Clientset, want int) []corev1.Node {
	t.Helper()
	var nodes []corev1.Node
	err := poll(ctx, func() (bool, error) {
		list, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		nodes = nodes[:0]
		for _, node := range list.Items {
			if isNodeReady(&node) {
				nodes = append(nodes, node)
			}
		}
		if len(nodes) < want {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("failed waiting for %d ready nodes: %v", want, err)
	}
	return nodes
}

func validateNodes(t *testing.T, nodes []corev1.Node, labelKey string) {
	t.Helper()
	schedulable := 0
	var badIDs []string
	for _, node := range nodes {
		if isNodeReady(&node) && !node.Spec.Unschedulable {
			schedulable++
		}
		labelVal := ""
		if labelKey != "" && node.Labels != nil {
			labelVal = node.Labels[labelKey]
		}
		t.Logf("node=%s ready=%t unschedulable=%t providerID=%q label=%q", node.Name, isNodeReady(&node), node.Spec.Unschedulable, node.Spec.ProviderID, labelVal)
		if _, err := parseProviderID(node.Spec.ProviderID); err != nil {
			badIDs = append(badIDs, fmt.Sprintf("%s=%q", node.Name, node.Spec.ProviderID))
		}
	}
	if schedulable < 2 {
		t.Fatalf("expected at least 2 schedulable nodes, got %d; check for cordoned nodes", schedulable)
	}
	if len(badIDs) > 0 {
		t.Fatalf("nodes missing valid providerID: %s", strings.Join(badIDs, ", "))
	}
}

func resetNodeState(ctx context.Context, t *testing.T, client *kubernetes.Clientset, labelKey, setupAnnotation string) {
	t.Helper()
	list, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list nodes for reset: %v", err)
	}
	for _, node := range list.Items {
		updated := node.DeepCopy()
		changed := false
		if updated.Spec.Unschedulable {
			updated.Spec.Unschedulable = false
			changed = true
		}
		if len(updated.Spec.Taints) > 0 {
			updated.Spec.Taints = nil
			changed = true
		}
		if labelKey != "" && updated.Labels != nil {
			if _, ok := updated.Labels[labelKey]; ok {
				delete(updated.Labels, labelKey)
				changed = true
			}
		}
		if setupAnnotation != "" && updated.Annotations != nil {
			if _, ok := updated.Annotations[setupAnnotation]; ok {
				delete(updated.Annotations, setupAnnotation)
				changed = true
			}
		}
		if !changed {
			continue
		}
		if _, err := client.CoreV1().Nodes().Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("failed to reset node %s: %v", node.Name, err)
		}
	}
}

func hasLabelFreeNode(kube *kubernetes.Clientset, labelKey string) bool {
	nodes, err := kube.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return false
	}
	for _, node := range nodes.Items {
		if !isNodeReady(&node) || node.Spec.Unschedulable {
			continue
		}
		if node.Labels[labelKey] == "" {
			return true
		}
	}
	return false
}

func clearStaleLabels(ctx context.Context, t *testing.T, client *kubernetes.Clientset, labelKey string, fips []*hcloud.FloatingIP) {
	t.Helper()
	if labelKey == "" {
		return
	}
	ipSet := map[string]struct{}{}
	for _, fip := range fips {
		if fip == nil {
			continue
		}
		ipSet[fip.IP.String()] = struct{}{}
	}
	list, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to list nodes for label cleanup: %v", err)
	}
	for _, node := range list.Items {
		val := ""
		if node.Labels != nil {
			val = node.Labels[labelKey]
		}
		if val == "" {
			continue
		}
		if _, ok := ipSet[val]; ok {
			continue
		}
		updated := node.DeepCopy()
		delete(updated.Labels, labelKey)
		if _, err := client.CoreV1().Nodes().Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("failed to clear stale label on node %s: %v", node.Name, err)
		}
	}
}

func isNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func nodeServerID(t *testing.T, node corev1.Node) int {
	t.Helper()
	if strings.HasPrefix(node.Spec.ProviderID, "hcloud://") {
		raw := strings.TrimPrefix(node.Spec.ProviderID, "hcloud://")
		id, err := strconv.Atoi(raw)
		if err == nil && id > 0 {
			return id
		}
	}
	t.Fatalf("node %s has invalid providerID %q", node.Name, node.Spec.ProviderID)
	return 0
}

func nodeLocation(ctx context.Context, t *testing.T, client *hcloud.Client, serverID int) *hcloud.Location {
	t.Helper()
	server, _, err := client.Server.GetByID(ctx, serverID)
	if err != nil {
		t.Logf("failed to fetch server %d: %v", serverID, err)
		return nil
	}
	if server == nil || server.Datacenter == nil || server.Datacenter.Location == nil {
		t.Logf("server %d missing datacenter/location info; creating floating IP without a home location", serverID)
		return nil
	}
	return server.Datacenter.Location
}

func locationFromNodeLabels(ctx context.Context, t *testing.T, client *hcloud.Client, nodes []corev1.Node) *hcloud.Location {
	t.Helper()
	var region string
	for _, node := range nodes {
		if val := node.Labels["topology.kubernetes.io/region"]; val != "" {
			region = val
			break
		}
		if val := node.Labels["failure-domain.beta.kubernetes.io/region"]; val != "" {
			region = val
			break
		}
	}
	if region == "" {
		t.Logf("no region label found on nodes")
		return nil
	}
	location, _, err := client.Location.GetByName(ctx, region)
	if err != nil {
		t.Fatalf("failed to fetch hcloud location %q: %v", region, err)
	}
	if location == nil {
		t.Fatalf("hcloud location %q not found", region)
	}
	return location
}

func tokenMatchesCluster(ctx context.Context, t *testing.T, client *hcloud.Client, nodes []corev1.Node) bool {
	t.Helper()
	if len(nodes) == 0 {
		t.Fatalf("no nodes available to validate hcloud token")
	}
	serverID := nodeServerID(t, nodes[0])
	server, _, err := client.Server.GetByID(ctx, serverID)
	if err == nil {
		return server != nil
	}
	if hcloud.IsError(err, hcloud.ErrorCodeNotFound) {
		return false
	}
	t.Fatalf("failed to validate hcloud token for server %d: %v", serverID, err)
	return false
}

func secretToken(ctx context.Context, t *testing.T, client *kubernetes.Clientset, namespace string) string {
	t.Helper()
	if namespace == "" {
		return ""
	}
	secret, err := client.CoreV1().Secrets(namespace).Get(ctx, "hcloud", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to read secret %s/hcloud: %v", namespace, err)
	}
	if secret.Data == nil {
		return ""
	}
	return strings.TrimSpace(string(secret.Data["token"]))
}

func parseSelector(t *testing.T, selector string) map[string]string {
	t.Helper()
	labels := map[string]string{}
	parts := strings.Split(selector, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		keyVal := strings.SplitN(part, "=", 2)
		if len(keyVal) != 2 || keyVal[0] == "" || keyVal[1] == "" {
			t.Fatalf("selector %q must be key=value[,key=value]", selector)
		}
		labels[keyVal[0]] = keyVal[1]
	}
	if len(labels) == 0 {
		t.Fatalf("selector %q produced no labels", selector)
	}
	return labels
}

func createFloatingIPs(ctx context.Context, t *testing.T, client *hcloud.Client, labels map[string]string, location *hcloud.Location, fallbackServerID int, count int) []*hcloud.FloatingIP {
	t.Helper()
	var fips []*hcloud.FloatingIP
	for i := 0; i < count; i++ {
		description := fmt.Sprintf("hcloud-fip-k8s e2e %d", time.Now().UnixNano())
		opts := hcloud.FloatingIPCreateOpts{
			Type:        hcloud.FloatingIPTypeIPv4,
			Description: &description,
			Labels:      labels,
		}
		if location != nil {
			opts.HomeLocation = location
		} else if fallbackServerID > 0 {
			opts.Server = &hcloud.Server{ID: fallbackServerID}
		}
		result, _, err := client.FloatingIP.Create(ctx, opts)
		if err != nil {
			t.Fatalf("failed to create floating IP: %v", err)
		}
		if result.FloatingIP == nil {
			t.Fatalf("floating IP create returned nil")
		}
		fips = append(fips, result.FloatingIP)
	}
	return fips
}

func ensureFloatingIPs(ctx context.Context, t *testing.T, client *hcloud.Client, selector string, labels map[string]string, location *hcloud.Location, fallbackServerID int, count int) ([]*hcloud.FloatingIP, []*hcloud.FloatingIP) {
	t.Helper()
	existing := listFloatingIPs(ctx, t, client, selector)
	if len(existing) > count {
		t.Logf("found %d floating IPs for selector %q; deleting extras", len(existing), selector)
		extras := existing[count:]
		deleteFloatingIPs(ctx, t, client, extras)
		existing = waitForFloatingIPCount(ctx, t, client, selector, count)
	}
	fips := append([]*hcloud.FloatingIP{}, existing...)
	var created []*hcloud.FloatingIP
	if len(fips) < count {
		created = createFloatingIPs(ctx, t, client, labels, location, fallbackServerID, count-len(fips))
		fips = waitForFloatingIPCount(ctx, t, client, selector, count)
	} else {
		fips = waitForFloatingIPCount(ctx, t, client, selector, count)
	}
	return fips, created
}

func listFloatingIPs(ctx context.Context, t *testing.T, client *hcloud.Client, selector string) []*hcloud.FloatingIP {
	t.Helper()
	var fips []*hcloud.FloatingIP
	page := 1
	lastPage := 2
	for page < lastPage {
		fipPage, resp, err := client.FloatingIP.List(ctx, hcloud.FloatingIPListOpts{
			ListOpts: hcloud.ListOpts{
				Page:          page,
				LabelSelector: selector,
			},
		})
		if err != nil {
			t.Fatalf("failed listing floating IPs: %v", err)
		}
		fips = append(fips, fipPage...)
		page = resp.Meta.Pagination.Page + 1
		lastPage = resp.Meta.Pagination.LastPage
	}
	return fips
}

func waitForFloatingIPCount(ctx context.Context, t *testing.T, client *hcloud.Client, selector string, count int) []*hcloud.FloatingIP {
	t.Helper()
	var last []*hcloud.FloatingIP
	err := poll(ctx, func() (bool, error) {
		last = listFloatingIPs(ctx, t, client, selector)
		return len(last) <= count, nil
	})
	if err != nil {
		t.Fatalf("failed waiting for floating IP count %d (selector %q): %v", count, selector, err)
	}
	if len(last) > count {
		t.Fatalf("floating IP count %d still exceeds expected %d for selector %q", len(last), count, selector)
	}
	return last
}

func resetFloatingIPs(ctx context.Context, t *testing.T, client *hcloud.Client, fips []*hcloud.FloatingIP) {
	t.Helper()
	for _, fip := range fips {
		if fip == nil || fip.Server == nil {
			continue
		}
		action, _, err := client.FloatingIP.Unassign(ctx, fip)
		if err != nil {
			t.Fatalf("failed to unassign floating IP %d: %v", fip.ID, err)
		}
		waitForAction(ctx, t, client, action, fmt.Sprintf("unassign floating IP %d", fip.ID))
	}
}

func deleteFloatingIPs(ctx context.Context, t *testing.T, client *hcloud.Client, fips []*hcloud.FloatingIP) {
	t.Helper()
	for _, fip := range fips {
		if fip == nil {
			continue
		}
		if _, err := client.FloatingIP.Delete(ctx, fip); err != nil {
			t.Logf("failed to delete floating IP %d: %v", fip.ID, err)
		}
	}
}

func waitForAssignment(ctx context.Context, t *testing.T, kube *kubernetes.Clientset, client *hcloud.Client, fipID int, labelKey, avoidNode string) string {
	t.Helper()
	var assignedNode string
	start := time.Now()
	lastLog := time.Now()
	err := poll(ctx, func() (bool, error) {
		fip, _, err := client.FloatingIP.GetByID(ctx, fipID)
		if err != nil {
			return false, err
		}
		if fip == nil || fip.Server == nil {
			logAssignmentWait(t, start, &lastLog, "waiting for hcloud assignment", "")
			return false, nil
		}

		nodes, err := kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}

		target, err := nodeByServerID(nodes.Items, fip.Server.ID)
		if err != nil {
			logAssignmentWait(t, start, &lastLog, "no node for server", fmt.Sprintf("server=%d ip=%s", fip.Server.ID, fip.IP.String()))
			return false, err
		}

		if target.Labels[labelKey] != fip.IP.String() {
			logAssignmentWait(t, start, &lastLog, "label not set", fmt.Sprintf("node=%s want=%s got=%s", target.Name, fip.IP.String(), target.Labels[labelKey]))
			return false, nil
		}
		if err := assertSingleLabel(nodes.Items, labelKey, fip.IP.String()); err != nil {
			logAssignmentWait(t, start, &lastLog, "label not unique", err.Error())
		}
		assignedNode = target.Name
		if avoidNode != "" && assignedNode == avoidNode {
			logAssignmentWait(t, start, &lastLog, "assignment still on avoided node", assignedNode)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("failed waiting for floating IP %d assignment: %v", fipID, err)
	}
	return assignedNode
}

func nodeByServerID(nodes []corev1.Node, serverID int) (*corev1.Node, error) {
	for i := range nodes {
		node := &nodes[i]
		id, err := parseProviderID(node.Spec.ProviderID)
		if err != nil {
			continue
		}
		if id == serverID {
			return node, nil
		}
	}
	return nil, fmt.Errorf("no node found for server %d", serverID)
}

func parseProviderID(providerID string) (int, error) {
	if strings.HasPrefix(providerID, "hcloud://") {
		raw := strings.TrimPrefix(providerID, "hcloud://")
		if raw != "" {
			return strconv.Atoi(raw)
		}
	}
	return 0, errors.New("invalid providerID")
}

func assertSingleLabel(nodes []corev1.Node, labelKey, labelValue string) error {
	count := 0
	for _, node := range nodes {
		if node.Labels[labelKey] == labelValue {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("expected exactly one node with label %s=%s, got %d", labelKey, labelValue, count)
	}
	return nil
}

func setNodeUnschedulable(ctx context.Context, client *kubernetes.Clientset, nodeName string, unschedulable bool) error {
	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if node.Spec.Unschedulable == unschedulable {
		return nil
	}
	node.Spec.Unschedulable = unschedulable
	_, err = client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	return err
}

func poll(ctx context.Context, fn func() (bool, error)) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		ok, err := fn()
		if ok {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			return lastErr
		case <-ticker.C:
		}
	}
}

func logAssignmentWait(t *testing.T, start time.Time, lastLog *time.Time, msg, details string) {
	if time.Since(*lastLog) < logInterval {
		return
	}
	*lastLog = time.Now()
	if details != "" {
		t.Logf("waiting for assignment (%s): %s (elapsed=%s)", msg, details, time.Since(start).Truncate(time.Second))
	} else {
		t.Logf("waiting for assignment (%s) (elapsed=%s)", msg, time.Since(start).Truncate(time.Second))
	}
}

func waitForEvent(ctx context.Context, t *testing.T, kube *kubernetes.Clientset, namespace, reason, message string) {
	t.Helper()
	if namespace == "" {
		t.Fatalf("event namespace must be set")
	}
	start := time.Now()
	lastLog := time.Now()
	logDetail := message
	if logDetail == "" {
		logDetail = reason
	}
	err := poll(ctx, func() (bool, error) {
		list, err := kube.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		for _, event := range list.Items {
			if reason != "" && event.Reason != reason {
				continue
			}
			if message != "" && !strings.Contains(event.Message, message) {
				continue
			}
			if reason == "" && message == "" {
				continue
			}
			t.Logf("found event %s/%s: reason=%s message=%s", event.Namespace, event.Name, event.Reason, event.Message)
			return true, nil
		}
		logAssignmentWait(t, start, &lastLog, "waiting for event", logDetail)
		return false, nil
	})
	if err != nil {
		t.Fatalf("failed waiting for event reason %q message %q in namespace %s: %v", reason, message, namespace, err)
	}
}

func waitForAction(ctx context.Context, t *testing.T, client *hcloud.Client, action *hcloud.Action, desc string) {
	t.Helper()
	if action == nil {
		return
	}
	_, errCh := client.Action.WatchProgress(ctx, action)
	if err := <-errCh; err != nil {
		t.Fatalf("action %s failed: %v", desc, err)
	}
}
