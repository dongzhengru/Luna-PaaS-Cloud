package model

import "time"

type Base struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	CreatedAt time.Time `                          json:"created_at"`
	UpdatedAt time.Time `                          json:"updated_at"`
}
type AllowedUser struct {
	Base
	Phone   string `gorm:"uniqueIndex;size:32" json:"phone"`
	Enabled bool   `gorm:"default:true"        json:"enabled"`
}
type Session struct {
	Base
	TokenHash string    `gorm:"uniqueIndex;size:64"`
	Phone     string    `gorm:"index;size:32"`
	ExpiresAt time.Time `gorm:"index"`
}
type OAuthState struct {
	Base
	StateHash string    `gorm:"uniqueIndex;size:64"`
	ExpiresAt time.Time `gorm:"index"`
}
type SecretSetting struct {
	Key            string    `gorm:"primaryKey;size:64" json:"key"`
	EncryptedValue string    `gorm:"type:text"          json:"-"`
	UpdatedAt      time.Time `                          json:"updated_at"`
}

type Node struct {
	Base
	Name                string `gorm:"uniqueIndex;size:100" json:"name"`
	Type                string `gorm:"size:16"              json:"type"`
	Host                string `                            json:"host"`
	Port                int    `                            json:"port"`
	Username            string `                            json:"username"`
	AuthType            string `                            json:"auth_type"`
	EncryptedPassword   string `                            json:"-"`
	EncryptedPrivateKey string `gorm:"type:longtext"        json:"-"`
	EncryptedPassphrase string `                            json:"-"`
	HostKey             string `gorm:"type:text"            json:"host_key"`
	DeployRoot          string `                            json:"deploy_root"`
	AllowedMountRoots   string `gorm:"type:text"            json:"allowed_mount_roots"`
	ExecutorType        string `gorm:"default:compose"      json:"executor_type"`
	Status              string `                            json:"status"`
	LastError           string `gorm:"type:text"            json:"last_error"`
}

type App struct {
	Base
	Name                 string `gorm:"uniqueIndex;size:100" json:"name"`
	Slug                 string `gorm:"uniqueIndex;size:100" json:"slug"`
	Type                 string `gorm:"size:16"              json:"type"`
	RuntimeVersion       string `gorm:"size:16"              json:"runtime_version"`
	RepoURL              string `gorm:"uniqueIndex;size:500" json:"repo_url"`
	RepoOwner            string `                            json:"repo_owner"`
	RepoName             string `                            json:"repo_name"`
	Branch               string `                            json:"branch"`
	DockerfilePath       string `                            json:"dockerfile_path"`
	BuildContext         string `                            json:"build_context"`
	NodeID               string `gorm:"index;size:36"        json:"node_id"`
	HostPort             int    `gorm:"index"                json:"host_port"`
	ContainerPort        int    `                            json:"container_port"`
	RestartPolicy        string `                            json:"restart_policy"`
	HostAccessEnabled    bool   `gorm:"default:false"        json:"host_access_enabled"`
	EnvironmentJSON      string `gorm:"type:longtext"        json:"-"`
	VolumesJSON          string `gorm:"type:longtext"        json:"volumes_json"`
	HealthJSON           string `gorm:"type:text"            json:"health_json"`
	CallbackSecret       string `                            json:"-"`
	InitialDeployPending bool   `                            json:"initial_deploy_pending"`
	Status               string `gorm:"index;size:32"        json:"status"`
	LastError            string `gorm:"type:text"            json:"last_error"`
	ActiveReleaseID      string `gorm:"size:36"              json:"active_release_id"`
	WorkflowSHA          string `                            json:"-"`
}

type Build struct {
	Base
	AppID      string `gorm:"uniqueIndex:run_attempt;size:36" json:"app_id"`
	RunID      int64  `gorm:"uniqueIndex:run_attempt"         json:"run_id"`
	RunAttempt int    `gorm:"uniqueIndex:run_attempt"         json:"run_attempt"`
	CommitSHA  string `                                       json:"commit_sha"`
	Ref        string `                                       json:"ref"`
	Image      string `                                       json:"image"`
	Status     string `gorm:"index"                           json:"status"`
	Initial    bool   `                                       json:"initial"`
	HTMLURL    string `                                       json:"html_url"`
	Error      string `gorm:"type:text"                       json:"error"`
}
type Release struct {
	Base
	AppID             string `gorm:"index;size:36" json:"app_id"`
	BuildID           string `gorm:"index;size:36" json:"build_id"`
	Image             string `                     json:"image"`
	ConfigSnapshot    string `gorm:"type:longtext" json:"-"`
	Status            string `gorm:"index"         json:"status"`
	Logs              string `gorm:"type:longtext" json:"logs"`
	PreviousReleaseID string `gorm:"size:36"       json:"previous_release_id"`
	RollbackOf        string `gorm:"size:36"       json:"rollback_of"`
}
type Task struct {
	Base
	Kind       string `json:"kind"`
	ResourceID string `json:"resource_id" gorm:"index;size:36"`
	Status     string `json:"status"      gorm:"index"`
	Error      string `json:"error"       gorm:"type:text"`
	Logs       string `json:"logs"        gorm:"type:longtext"`
}

type EnvironmentVar struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}
type Volume struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}
type HealthCheck struct {
	Command     string `json:"command"`
	Interval    string `json:"interval"`
	Timeout     string `json:"timeout"`
	Retries     int    `json:"retries"`
	StartPeriod string `json:"start_period"`
}
