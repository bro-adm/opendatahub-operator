package dscinitialization

// +kubebuilder:rbac:groups="dscinitialization.opendatahub.io",resources=dscinitializations/status,verbs=get;update;patch;delete
// +kubebuilder:rbac:groups="dscinitialization.opendatahub.io",resources=dscinitializations/finalizers,verbs=get;update;patch;delete
// +kubebuilder:rbac:groups="dscinitialization.opendatahub.io",resources=dscinitializations,verbs=get;list;watch;create;update;patch;delete;deletecollection
// +kubebuilder:rbac:groups="features.opendatahub.io",resources=featuretrackers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="features.opendatahub.io",resources=featuretrackers/status,verbs=get;update;patch;delete
// +kubebuilder:rbac:groups="features.opendatahub.io",resources=featuretrackers/finalizers,verbs=update;patch;get

/* Auth */
// +kubebuilder:rbac:groups="config.openshift.io",resources=authentications;infrastructures,verbs=get;watch;list
// +kubebuilder:rbac:groups="config.openshift.io",resources=apiservers,verbs=get;watch;list

// Auth
// +kubebuilder:rbac:groups=services.platform.opendatahub.io,resources=auths,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=services.platform.opendatahub.io,resources=auths/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=services.platform.opendatahub.io,resources=auths/finalizers,verbs=update
