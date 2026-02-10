package terraform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/imyousuf/CodeEagle/internal/graph"
	"github.com/imyousuf/CodeEagle/internal/parser"
)

const testTerraformSource = `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

variable "instance_type" {
  type        = string
  default     = "t3.micro"
  description = "EC2 instance type"
}

locals {
  name_prefix = "myapp"
  env         = "production"
}

resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = var.instance_type
}

data "aws_ami" "latest" {
  most_recent = true
  owners      = ["amazon"]
}

module "vpc" {
  source = "./modules/vpc"
  cidr   = "10.0.0.0/16"
}

module "rds" {
  source  = "terraform-aws-modules/rds/aws"
  version = "~> 5.0"
}

output "instance_ip" {
  value       = aws_instance.web.public_ip
  description = "The public IP of the instance"
}
`

func TestParseTerraformFile(t *testing.T) {
	p := NewParser()

	result, err := p.ParseFile("infra/main.tf", []byte(testTerraformSource))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if result.Language != parser.LangTerraform {
		t.Errorf("Language = %q, want %q", result.Language, parser.LangTerraform)
	}

	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	// 1 file
	assertCount(t, counts, graph.NodeFile, 1)
	// 1 resource: aws_instance.web
	assertCount(t, counts, graph.NodeStruct, 2) // resource + data source
	// 2 modules: vpc, rds
	assertCount(t, counts, graph.NodeModule, 3) // vpc + rds + vpc local source
	// 1 variable: instance_type + 2 locals = 3
	assertCount(t, counts, graph.NodeVariable, 3)
	// 1 output: instance_ip
	assertCount(t, counts, graph.NodeConstant, 1)
	// 1 provider: aws + 1 required_provider: aws = 2
	assertCount(t, counts, graph.NodeDependency, 3) // provider + required_provider + rds remote source

	nodeByName := indexByName(result.Nodes)

	// Check resource.
	if n, ok := nodeByName["aws_instance.web"]; ok {
		if n.Properties["kind"] != "resource" {
			t.Errorf("aws_instance.web kind = %q, want %q", n.Properties["kind"], "resource")
		}
		if n.Properties["resource_type"] != "aws_instance" {
			t.Errorf("resource_type = %q, want %q", n.Properties["resource_type"], "aws_instance")
		}
	} else {
		t.Error("expected aws_instance.web resource node")
	}

	// Check data source.
	if n, ok := nodeByName["data.aws_ami.latest"]; ok {
		if n.Properties["kind"] != "data_source" {
			t.Errorf("data.aws_ami.latest kind = %q, want %q", n.Properties["kind"], "data_source")
		}
	} else {
		t.Error("expected data.aws_ami.latest data source node")
	}

	// Check module.
	if n, ok := nodeByName["vpc"]; ok {
		if n.Properties["kind"] != "module" {
			t.Errorf("vpc kind = %q, want %q", n.Properties["kind"], "module")
		}
		if n.Properties["source"] != "./modules/vpc" {
			t.Errorf("vpc source = %q, want %q", n.Properties["source"], "./modules/vpc")
		}
	} else {
		t.Error("expected vpc module node")
	}

	// Check variable.
	if n, ok := nodeByName["instance_type"]; ok {
		if n.Properties["kind"] != "terraform_var" {
			t.Errorf("instance_type kind = %q, want %q", n.Properties["kind"], "terraform_var")
		}
		if n.DocComment != "EC2 instance type" {
			t.Errorf("instance_type description = %q, want %q", n.DocComment, "EC2 instance type")
		}
	} else {
		t.Error("expected instance_type variable node")
	}

	// Check locals.
	if _, ok := nodeByName["local.name_prefix"]; !ok {
		t.Error("expected local.name_prefix variable node")
	}
	if _, ok := nodeByName["local.env"]; !ok {
		t.Error("expected local.env variable node")
	}

	// Check output.
	if n, ok := nodeByName["instance_ip"]; ok {
		if n.Properties["kind"] != "output" {
			t.Errorf("instance_ip kind = %q, want %q", n.Properties["kind"], "output")
		}
		if n.DocComment != "The public IP of the instance" {
			t.Errorf("instance_ip description = %q, want %q", n.DocComment, "The public IP of the instance")
		}
	} else {
		t.Error("expected instance_ip output node")
	}

	// Check provider.
	if n, ok := nodeByName["aws"]; ok {
		if n.Properties["kind"] != "provider" {
			t.Errorf("aws provider kind = %q, want %q", n.Properties["kind"], "provider")
		}
		if n.Properties["region"] != "us-east-1" {
			t.Errorf("aws region = %q, want %q", n.Properties["region"], "us-east-1")
		}
	} else {
		t.Error("expected aws provider node")
	}

	// Check DependsOn edges (resource -> provider, data -> provider, modules -> sources).
	depEdges := 0
	for _, edge := range result.Edges {
		if edge.Type == graph.EdgeDependsOn {
			depEdges++
		}
	}
	// aws_instance.web -> aws provider, data.aws_ami.latest -> aws provider,
	// vpc -> local source, rds -> remote source = 4
	if depEdges < 4 {
		t.Errorf("DependsOn edges = %d, want at least 4", depEdges)
	}
}

func TestParseTfvars(t *testing.T) {
	p := NewParser()
	src := `instance_type = "t3.large"
environment   = "staging"
region        = "us-west-2"
`
	result, err := p.ParseFile("terraform.tfvars", []byte(src))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}

	assertCount(t, counts, graph.NodeFile, 1)
	assertCount(t, counts, graph.NodeVariable, 3)

	nodeByName := indexByName(result.Nodes)
	if n, ok := nodeByName["instance_type"]; ok {
		if n.Properties["value"] != "t3.large" {
			t.Errorf("instance_type value = %q, want %q", n.Properties["value"], "t3.large")
		}
	} else {
		t.Error("expected instance_type variable node")
	}
}

func TestLanguageAndExtensions(t *testing.T) {
	p := NewParser()
	if p.Language() != parser.LangTerraform {
		t.Errorf("Language() = %q, want %q", p.Language(), parser.LangTerraform)
	}
	exts := p.Extensions()
	if len(exts) != 2 || exts[0] != ".tf" {
		t.Errorf("Extensions() = %v, want [\".tf\", \".tfvars\"]", exts)
	}
}

func TestParseTerraformFixture(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")

	// Test .tf fixture.
	tfPath := filepath.Join(projectRoot, "testdata", "sample.tf")
	content, err := os.ReadFile(tfPath)
	if err != nil {
		t.Skipf("testdata/sample.tf not found: %v", err)
	}

	p := NewParser()
	result, err := p.ParseFile("testdata/sample.tf", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	nodeByName := indexByName(result.Nodes)

	// Check resources.
	for _, name := range []string{"aws_instance.web", "aws_security_group.web_sg"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected resource %q in fixture", name)
		}
	}

	// Check modules.
	for _, name := range []string{"vpc", "rds"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected module %q in fixture", name)
		}
	}

	// Check outputs.
	for _, name := range []string{"instance_ip", "sg_id"} {
		if _, ok := nodeByName[name]; !ok {
			t.Errorf("expected output %q in fixture", name)
		}
	}

	// Test .tfvars fixture.
	tfvarsPath := filepath.Join(projectRoot, "testdata", "sample.tfvars")
	content, err = os.ReadFile(tfvarsPath)
	if err != nil {
		t.Skipf("testdata/sample.tfvars not found: %v", err)
	}

	result, err = p.ParseFile("testdata/sample.tfvars", content)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	counts := make(map[graph.NodeType]int)
	for _, n := range result.Nodes {
		counts[n.Type]++
	}
	assertCount(t, counts, graph.NodeVariable, 3)
}

// Helpers

func assertCount(t *testing.T, counts map[graph.NodeType]int, nt graph.NodeType, want int) {
	t.Helper()
	if counts[nt] != want {
		t.Errorf("%s count = %d, want %d", nt, counts[nt], want)
	}
}

func indexByName(nodes []*graph.Node) map[string]*graph.Node {
	m := make(map[string]*graph.Node, len(nodes))
	for _, n := range nodes {
		m[n.Name] = n
	}
	return m
}
