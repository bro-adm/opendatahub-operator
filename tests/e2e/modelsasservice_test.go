package e2e_test

import (
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentApi "github.com/opendatahub-io/opendatahub-operator/v2/api/components/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/v2/internal/controller/components/modelsasservice"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/testf"

	. "github.com/onsi/gomega"
)

type ModelsAsServiceTestCtx struct {
	*ComponentTestCtx
}

const (
	// Subcomponent field name in JSON (matches the field name in KserveCommonSpec).
	// This must match the JSON tag in KserveCommonSpec.ModelsAsService.
	modelsAsServiceFieldName = "modelsAsService"

	// Gateway constants from modelsasservice package.
	maasGatewayNamespace = modelsasservice.DefaultGatewayNamespace
	maasGatewayName      = modelsasservice.DefaultGatewayName

	// Gateway class for OpenShift default ingress controller.
	// Reference: https://github.com/opendatahub-io/models-as-a-service/blob/main/deployment/base/networking/maas/maas-gateway-api.yaml
	maasGatewayClassName = "openshift-default"

	// PostgreSQL constants for the MaaS database dependency.
	// Reference: https://github.com/opendatahub-io/models-as-a-service/blob/main/scripts/deploy.sh
	maasPostgresName     = "maas-postgres"
	maasPostgresImage    = "registry.redhat.io/rhel9/postgresql-15:latest"
	maasPostgresUser     = "maas"
	maasPostgresPassword = "maas-e2e-test" //nolint:gosec // test-only credential, not a real secret
	maasPostgresDB       = "maas"
	maasDBConfigSecret   = "maas-db-config" //nolint:gosec // secret name reference, not a credential

	// Test telemetry endpoint constants.
	testLokiEndpoint = "https://loki.example.com"
)

func modelsAsServiceTestSuite(t *testing.T) {
	t.Helper()

	ct, err := NewSubComponentTestCtx(t, &componentApi.ModelsAsService{}, componentApi.KserveKind, modelsAsServiceFieldName)
	require.NoError(t, err)

	componentCtx := ModelsAsServiceTestCtx{
		ComponentTestCtx: ct,
	}

	// Setup: Create PostgreSQL and the MaaS Gateway before running tests.
	// PostgreSQL must be created before enabling the component because
	// maas-api reads the maas-db-config secret on startup.
	componentCtx.createMaaSPostgres(t)
	componentCtx.createMaaSGateway(t)

	// Note: per e2e convention, do not cleanup resources; leave state for debugging.

	testCases := []TestCase{
		{"Validate subcomponent enabled", componentCtx.ValidateSubComponentEnabled},
		{"Validate operands have OwnerReferences", componentCtx.ValidateOperandsOwnerReferences},

		// Observability resource tests
		{"Validate observability resources with telemetry", componentCtx.ValidateObservabilityResourcesWithTelemetry},
		{"Validate observability OwnerReferences", componentCtx.ValidateObservabilityOwnerReferences},
		{"Validate observability resource deletion recovery", componentCtx.ValidateObservabilityResourceDeletion},
		{"Validate observability resources without telemetry", componentCtx.ValidateObservabilityResourcesWithoutTelemetry},

		{"Validate update operand resources", componentCtx.ValidateUpdateDeploymentsResources},
		{"Validate subcomponent releases", componentCtx.ValidateSubComponentReleases},
		{"Validate resource deletion recovery", componentCtx.ValidateAllDeletionRecovery},
		{"Validate subcomponent disabled", componentCtx.ValidateSubComponentDisabled},
	}

	RunTestCases(t, testCases)
}

// createMaaSPostgres creates a minimal PostgreSQL instance and the maas-db-config secret
// required by the maas-api component. This mirrors the POC-grade setup from:
// https://github.com/opendatahub-io/models-as-a-service/blob/main/scripts/deploy.sh
func (tc *ModelsAsServiceTestCtx) createMaaSPostgres(t *testing.T) {
	t.Helper()

	ns := tc.AppsNamespace
	t.Logf("Creating MaaS PostgreSQL instance in namespace %s", ns)

	// Create the postgres Deployment
	tc.EventuallyResourceCreatedOrUpdated(
		WithMinimalObject(gvk.Deployment, types.NamespacedName{Name: maasPostgresName, Namespace: ns}),
		WithTransforms(
			testf.Transform(`.metadata.labels = {"app": "%s"}`, maasPostgresName),
			testf.Transform(`.spec.replicas = 1`),
			testf.Transform(`.spec.selector.matchLabels = {"app": "%s"}`, maasPostgresName),
			testf.Transform(`.spec.template.metadata.labels = {"app": "%s"}`, maasPostgresName),
			testf.Transform(`.spec.template.spec.containers = [
				{
					"name": "postgres",
					"image": "%s",
					"env": [
						{"name": "POSTGRESQL_USER", "value": "%s"},
						{"name": "POSTGRESQL_PASSWORD", "value": "%s"},
						{"name": "POSTGRESQL_DATABASE", "value": "%s"}
					],
					"ports": [{"containerPort": 5432}],
					"volumeMounts": [{"name": "data", "mountPath": "/var/lib/pgsql/data"}],
					"resources": {
						"requests": {"memory": "256Mi", "cpu": "100m"},
						"limits": {"memory": "512Mi", "cpu": "500m"}
					},
					"readinessProbe": {
						"exec": {"command": ["/usr/libexec/check-container"]},
						"initialDelaySeconds": 5,
						"periodSeconds": 5
					}
				}
			]`, maasPostgresImage, maasPostgresUser, maasPostgresPassword, maasPostgresDB),
			testf.Transform(`.spec.template.spec.volumes = [{"name": "data", "emptyDir": {}}]`),
		),
		WithCustomErrorMsg("Failed to create PostgreSQL Deployment"),
	)

	// Create the postgres Service
	tc.EventuallyResourceCreatedOrUpdated(
		WithMinimalObject(gvk.Service, types.NamespacedName{Name: maasPostgresName, Namespace: ns}),
		WithTransforms(
			testf.Transform(`.metadata.labels = {"app": "%s"}`, maasPostgresName),
			testf.Transform(`.spec.selector = {"app": "%s"}`, maasPostgresName),
			testf.Transform(`.spec.ports = [{"port": 5432, "targetPort": 5432}]`),
		),
		WithCustomErrorMsg("Failed to create PostgreSQL Service"),
	)

	// Create the maas-db-config secret with the connection URL
	dbURL := fmt.Sprintf("postgresql://%s:%s@%s/%s?sslmode=disable",
		maasPostgresUser, maasPostgresPassword, net.JoinHostPort(maasPostgresName, "5432"), maasPostgresDB)

	tc.EventuallyResourceCreatedOrUpdated(
		WithMinimalObject(gvk.Secret, types.NamespacedName{Name: maasDBConfigSecret, Namespace: ns}),
		WithTransforms(
			testf.Transform(`.stringData = {"DB_CONNECTION_URL": "%s"}`, dbURL),
		),
		WithCustomErrorMsg("Failed to create maas-db-config Secret"),
	)

	// Wait for the postgres deployment to become available before proceeding,
	// since maas-api reads the database on startup. Use EnsureResourceExists
	// (which polls via Eventually) rather than EnsureDeploymentReady (point-in-time)
	// because the image pull may take time on first run.
	tc.EnsureResourceExists(
		WithMinimalObject(gvk.Deployment, types.NamespacedName{Name: maasPostgresName, Namespace: ns}),
		WithCondition(jq.Match(`.status.conditions[] | select(.type == "Available") | .status == "True"`)),
		WithCustomErrorMsg("PostgreSQL Deployment %s/%s should be available", ns, maasPostgresName),
	)

	t.Logf("MaaS PostgreSQL instance and maas-db-config secret created in namespace %s", ns)
}

// createMaaSGateway creates the maas-default-gateway Gateway resource required by ModelsAsService.
// The Gateway is based on the MaaS Gateway definition from:
// https://github.com/opendatahub-io/models-as-a-service/blob/main/deployment/base/networking/maas/maas-gateway-api.yaml
func (tc *ModelsAsServiceTestCtx) createMaaSGateway(t *testing.T) {
	t.Helper()
	t.Logf("Creating MaaS Gateway: %s/%s", maasGatewayNamespace, maasGatewayName)

	// First, ensure the namespace exists
	tc.EventuallyResourceCreatedOrUpdated(
		WithMinimalObject(gvk.Namespace, types.NamespacedName{Name: maasGatewayNamespace}),
		WithCustomErrorMsg("Failed to create/ensure namespace %s for MaaS Gateway", maasGatewayNamespace),
	)

	// Get the cluster domain for the Gateway hostname
	clusterDomain, err := cluster.GetDomain(tc.Context(), tc.Client())
	require.NoError(t, err, "Failed to get cluster domain")

	hostname := fmt.Sprintf("maas.%s", clusterDomain)
	t.Logf("Using hostname for MaaS Gateway: %s", hostname)

	// Create the Gateway resource
	// Using testf.Transform to build the Gateway spec dynamically
	tc.EventuallyResourceCreatedOrUpdated(
		WithMinimalObject(gvk.KubernetesGateway, types.NamespacedName{
			Name:      maasGatewayName,
			Namespace: maasGatewayNamespace,
		}),
		WithMutateFunc(testf.TransformPipeline(
			// Set labels
			testf.Transform(`.metadata.labels = {
				"app.kubernetes.io/name": "maas",
				"app.kubernetes.io/instance": "%s",
				"app.kubernetes.io/component": "gateway",
				"opendatahub.io/managed": "false"
			}`, maasGatewayName),
			// Set annotations
			testf.Transform(`.metadata.annotations = {"opendatahub.io/managed": "false"}`),
			// Set the GatewayClass
			testf.Transform(`.spec.gatewayClassName = "%s"`, maasGatewayClassName),
			// Set the HTTP listener
			testf.Transform(`.spec.listeners = [
				{
					"name": "http",
					"hostname": "%s",
					"port": 80,
					"protocol": "HTTP",
					"allowedRoutes": {
						"namespaces": {
							"from": "All"
						}
					}
				}
			]`, hostname),
		)),
		WithCustomErrorMsg("Failed to create MaaS Gateway %s/%s", maasGatewayNamespace, maasGatewayName),
	)

	// Wait for the Gateway to exist
	tc.EnsureResourceExists(
		WithMinimalObject(gvk.KubernetesGateway, types.NamespacedName{
			Name:      maasGatewayName,
			Namespace: maasGatewayNamespace,
		}),
		WithCustomErrorMsg("MaaS Gateway %s/%s should exist", maasGatewayNamespace, maasGatewayName),
	)

	t.Logf("MaaS Gateway %s/%s created successfully", maasGatewayNamespace, maasGatewayName)
}

// enableTelemetryExports enables telemetry with Loki and/or Metering endpoints.
// Pass nil for endpoints you don't want to configure.
func (tc *ModelsAsServiceTestCtx) enableTelemetryExports(t *testing.T, lokiEndpoint, meteringEndpoint *string) {
	t.Helper()

	// Get the ModelsAsService CR
	maas := &componentApi.ModelsAsService{}
	err := tc.Client().Get(tc.Context(), types.NamespacedName{
		Name: componentApi.ModelsAsServiceInstanceName,
	}, maas)
	require.NoError(t, err, "Failed to get ModelsAsService CR")

	// Configure telemetry
	if maas.Spec.Telemetry == nil {
		maas.Spec.Telemetry = &componentApi.TelemetryConfig{}
	}
	enabled := true
	maas.Spec.Telemetry.Enabled = &enabled

	if lokiEndpoint != nil || meteringEndpoint != nil {
		maas.Spec.Telemetry.Exports = &componentApi.TelemetryExports{
			LokiEndpoint:     lokiEndpoint,
			MeteringEndpoint: meteringEndpoint,
		}
	}

	// Update the CR
	err = tc.Client().Update(tc.Context(), maas)
	require.NoError(t, err, "Failed to update ModelsAsService with telemetry config")

	t.Logf("Enabled telemetry exports: Loki=%v, Metering=%v", lokiEndpoint != nil, meteringEndpoint != nil)
}

// disableTelemetryExports removes telemetry export configuration.
func (tc *ModelsAsServiceTestCtx) disableTelemetryExports(t *testing.T) {
	t.Helper()

	// Get the ModelsAsService CR
	maas := &componentApi.ModelsAsService{}
	err := tc.Client().Get(tc.Context(), types.NamespacedName{
		Name: componentApi.ModelsAsServiceInstanceName,
	}, maas)
	require.NoError(t, err, "Failed to get ModelsAsService CR")

	// Remove exports configuration
	if maas.Spec.Telemetry != nil {
		maas.Spec.Telemetry.Exports = nil
	}

	// Update the CR
	err = tc.Client().Update(tc.Context(), maas)
	require.NoError(t, err, "Failed to disable telemetry exports")

	t.Log("Disabled telemetry exports")
}

// ValidateObservabilityResourcesWithTelemetry verifies that observability resources
// (OpenTelemetryCollector and EnvoyFilter) are created when telemetry exports are configured.
func (tc *ModelsAsServiceTestCtx) ValidateObservabilityResourcesWithTelemetry(t *testing.T) {
	t.Helper()

	// Configure telemetry with Loki endpoint
	lokiEndpoint := testLokiEndpoint
	tc.enableTelemetryExports(t, &lokiEndpoint, nil)

	// Wait for OpenTelemetryCollector to be created
	tc.EnsureResourceExists(
		WithMinimalObject(gvk.OpenTelemetryCollector, types.NamespacedName{
			Name:      modelsasservice.OTelCollectorName, // "user-usage"
			Namespace: tc.AppsNamespace,
		}),
		WithCondition(jq.Match(`.metadata.labels["app.kubernetes.io/part-of"] == "maas-observability"`)),
		WithCustomErrorMsg("OpenTelemetryCollector %s/%s should exist when telemetry exports configured",
			tc.AppsNamespace, modelsasservice.OTelCollectorName),
	)

	// Wait for EnvoyFilter to be created (in gateway namespace, not apps namespace)
	tc.EnsureResourceExists(
		WithMinimalObject(gvk.EnvoyFilter, types.NamespacedName{
			Name:      modelsasservice.GatewayEnvoyFilterName, // "maas-gateway-envoy-filter"
			Namespace: maasGatewayNamespace,                   // openshift-ingress
		}),
		WithCondition(jq.Match(`.metadata.labels["app.kubernetes.io/part-of"] == "maas-observability"`)),
		WithCustomErrorMsg("EnvoyFilter %s/%s should exist when telemetry exports configured",
			maasGatewayNamespace, modelsasservice.GatewayEnvoyFilterName),
	)

	t.Log("Observability resources (OpenTelemetryCollector, EnvoyFilter) created successfully")
}

// ValidateObservabilityResourcesWithoutTelemetry verifies that observability resources
// are NOT created when telemetry exports are not configured.
func (tc *ModelsAsServiceTestCtx) ValidateObservabilityResourcesWithoutTelemetry(t *testing.T) {
	t.Helper()

	// Disable telemetry exports (if previously enabled from other tests)
	tc.disableTelemetryExports(t)

	// Give the controller time to process the update and delete resources
	// Using EnsureResourcesGone with Eventually pattern
	tc.EnsureResourcesGone(
		WithMinimalObject(gvk.OpenTelemetryCollector, types.NamespacedName{Namespace: tc.AppsNamespace}),
		WithListOptions(&client.ListOptions{
			Namespace: tc.AppsNamespace,
		}),
		WithCondition(jq.Match(`.metadata.labels["app.kubernetes.io/part-of"] == "maas-observability"`)),
		WithCustomErrorMsg("OpenTelemetryCollector resources should not exist when telemetry exports disabled"),
	)

	tc.EnsureResourcesGone(
		WithMinimalObject(gvk.EnvoyFilter, types.NamespacedName{Namespace: maasGatewayNamespace}),
		WithListOptions(&client.ListOptions{
			Namespace: maasGatewayNamespace,
		}),
		WithCondition(jq.Match(`.metadata.labels["app.kubernetes.io/part-of"] == "maas-observability"`)),
		WithCustomErrorMsg("EnvoyFilter resources should not exist when telemetry exports disabled"),
	)

	t.Log("Verified observability resources absent when telemetry exports not configured")
}

// ValidateObservabilityOwnerReferences verifies that observability resources
// have correct OwnerReferences pointing to the ModelsAsService CR.
func (tc *ModelsAsServiceTestCtx) ValidateObservabilityOwnerReferences(t *testing.T) {
	t.Helper()

	// Ensure telemetry is enabled (may have been disabled by previous test)
	lokiEndpoint := testLokiEndpoint
	tc.enableTelemetryExports(t, &lokiEndpoint, nil)

	// Define expected owner reference condition
	observabilityOwnerRefCondition := And(
		jq.Match(`.metadata.ownerReferences | length == 1`),
		jq.Match(`.metadata.ownerReferences[0].kind == "ModelsAsService"`),
		jq.Match(`.metadata.ownerReferences[0].name == "%s"`, componentApi.ModelsAsServiceInstanceName),
		jq.Match(`.metadata.ownerReferences[0].controller == true`),
		jq.Match(`.metadata.ownerReferences[0].blockOwnerDeletion == true`),
	)

	// Verify OpenTelemetryCollector has OwnerReference
	tc.EnsureResourceExists(
		WithMinimalObject(gvk.OpenTelemetryCollector, types.NamespacedName{
			Name:      modelsasservice.OTelCollectorName,
			Namespace: tc.AppsNamespace,
		}),
		WithCondition(observabilityOwnerRefCondition),
		WithCustomErrorMsg("OpenTelemetryCollector should have OwnerReference to ModelsAsService"),
	)

	// Verify EnvoyFilter has OwnerReference (cross-namespace ownership)
	tc.EnsureResourceExists(
		WithMinimalObject(gvk.EnvoyFilter, types.NamespacedName{
			Name:      modelsasservice.GatewayEnvoyFilterName,
			Namespace: maasGatewayNamespace,
		}),
		WithCondition(observabilityOwnerRefCondition),
		WithCustomErrorMsg("EnvoyFilter should have OwnerReference to ModelsAsService"),
	)

	t.Log("Verified OwnerReferences on observability resources")
}

// ValidateObservabilityResourceDeletion tests deletion recovery for observability resources.
// The controller should recreate them due to OwnerReferences.
func (tc *ModelsAsServiceTestCtx) ValidateObservabilityResourceDeletion(t *testing.T) {
	t.Helper()

	// Ensure telemetry is enabled
	lokiEndpoint := testLokiEndpoint
	tc.enableTelemetryExports(t, &lokiEndpoint, nil)

	// Test OpenTelemetryCollector deletion recovery
	t.Run("OpenTelemetryCollector deletion recovery", func(t *testing.T) {
		tc.EnsureResourceDeletedThenRecreated(
			WithMinimalObject(gvk.OpenTelemetryCollector, types.NamespacedName{
				Name:      modelsasservice.OTelCollectorName,
				Namespace: tc.AppsNamespace,
			}),
		)
	})

	// Test EnvoyFilter deletion recovery
	t.Run("EnvoyFilter deletion recovery", func(t *testing.T) {
		tc.EnsureResourceDeletedThenRecreated(
			WithMinimalObject(gvk.EnvoyFilter, types.NamespacedName{
				Name:      modelsasservice.GatewayEnvoyFilterName,
				Namespace: maasGatewayNamespace,
			}),
		)
	})

	t.Log("Verified deletion recovery for observability resources")
}
