# Managed Postgres for the app -- deploy/k8s/'s own README already assumes
# "an existing, reachable Postgres 16 instance (a managed RDS/Cloud SQL
# instance...)" rather than running Postgres inside the cluster, this is
# that instance. db/migrations/ and the argusops_app/argusops_worker roles
# (db/init/*.sql) still need to be applied against it after this resource
# exists -- see the top-level deploy/README.md for that step; Terraform
# deliberately doesn't reach into the database's own schema/roles here.

resource "random_password" "db_master" {
  count   = var.db_master_password == "" ? 1 : 0
  length  = 24
  special = false # RDS master passwords reject some special characters
}

locals {
  db_master_password = var.db_master_password != "" ? var.db_master_password : random_password.db_master[0].result
}

resource "aws_db_subnet_group" "main" {
  name       = "${var.project}-${var.environment}"
  subnet_ids = aws_subnet.private[*].id

  tags = {
    Name = "${var.project}-${var.environment}"
  }
}

resource "aws_security_group" "db" {
  name_prefix = "${var.project}-${var.environment}-db-"
  vpc_id      = aws_vpc.main.id

  # Scoped to the VPC's own CIDR (both private and public subnets), not to a
  # specific node security group -- EKS managed node groups provision their
  # own SG implicitly, which Terraform can't cleanly reference here without
  # a custom launch template. Fine inside a single-VPC sample; tighten to
  # the actual node SG in a real deployment.
  ingress {
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.project}-${var.environment}-db"
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_db_instance" "main" {
  identifier     = "${var.project}-${var.environment}"
  engine         = "postgres"
  engine_version = var.db_engine_version
  instance_class = var.db_instance_class

  allocated_storage      = var.db_allocated_storage
  storage_type           = "gp3"
  storage_encrypted      = true
  db_name                = "argusops"
  username               = var.db_master_username
  password               = local.db_master_password
  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.db.id]

  # Sample-appropriate, not production-appropriate: no Multi-AZ, no read
  # replica, one backup a day, and Terraform is allowed to delete the final
  # snapshot on `terraform destroy` so tearing this sample down doesn't
  # leave a billed snapshot behind. Flip all three for anything real.
  multi_az                = false
  backup_retention_period = 1
  skip_final_snapshot     = true
  deletion_protection     = false

  tags = {
    Name = "${var.project}-${var.environment}"
  }
}
