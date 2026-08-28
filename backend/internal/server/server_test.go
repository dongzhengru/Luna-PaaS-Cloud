package server

import "testing"

func TestRepoParts(t *testing.T) {
	o, r, e := repoParts("https://github.com/Owner/My-App.git")
	if e != nil || o != "Owner" || r != "My-App" {
		t.Fatalf("%q %q %v", o, r, e)
	}
	if _, _, e = repoParts("https://gitlab.com/a/b"); e == nil {
		t.Fatal("accepted non-GitHub repository")
	}
}
func TestSlug(t *testing.T) {
	if got := slug("My API_服务"); got != "my-api" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateRuntime(t *testing.T) {
	if got, err := validateRuntime("java", "21"); err != nil || got != "21" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := validateRuntime("java", ""); err != nil || got != "8" {
		t.Fatalf("default got %q, %v", got, err)
	}
	if _, err := validateRuntime("java", "19"); err == nil {
		t.Fatal("accepted unsupported Java version")
	}
}

func TestAppName(t *testing.T) {
	for _, name := range []string{"api", "my-api2", "a"} {
		if !appNameRE.MatchString(name) {
			t.Fatalf("rejected valid app name %q", name)
		}
	}
	for _, name := range []string{"中文", "My-App", "2api", "my_api", ""} {
		if appNameRE.MatchString(name) {
			t.Fatalf("accepted invalid app name %q", name)
		}
	}
}

func TestValidateDingTalkWebhook(t *testing.T) {
	validWebhook := "https://oapi.dingtalk.com/robot/send?access_token=token"
	if err := validateDingTalkWebhook(validWebhook); err != nil {
		t.Fatal(err)
	}
	invalidWebhooks := []string{
		"http://oapi.dingtalk.com/robot/send?access_token=x",
		"https://example.com/robot/send?access_token=x",
		"https://oapi.dingtalk.com/robot/send",
	}
	for _, raw := range invalidWebhooks {
		if validateDingTalkWebhook(raw) == nil {
			t.Fatalf("accepted invalid webhook %q", raw)
		}
	}
}
