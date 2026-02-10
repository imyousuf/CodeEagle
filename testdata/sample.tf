terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.5"
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

variable "environment" {
  type        = string
  description = "Deployment environment"
}

locals {
  name_prefix = "myapp"
  env         = "production"
}

resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = var.instance_type

  tags = {
    Name = "${local.name_prefix}-web"
  }
}

resource "aws_security_group" "web_sg" {
  name        = "web-sg"
  description = "Security group for web instances"

  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
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

output "sg_id" {
  value = aws_security_group.web_sg.id
}
