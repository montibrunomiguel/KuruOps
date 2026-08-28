# Managed Postgres for the app -- same role as the AWS/GCP stacks' rds.tf /
# cloudsql.tf. VNet-integrated (private access only, no public endpoint),
# reachable from AKS over the shared VNet. db/migrations/ and the
# kuruops_app/kuruops_worker roles (db/init/*.sql) still need to be
# applied against it after this resource exists -- see the top-level
# deploy/README.md.

resource "random_password" "db_master" {
  count   = var.db_master_password == "" ? 1 : 0
  length  = 24
  special = false
}

locals {
  db_master_password = var.db_master_password != "" ? var.db_master_password : random_password.db_master[0].result
}

resource "azurerm_postgresql_flexible_server" "main" {
  name                = "${var.name}-${var.environment}"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location

  version    = var.db_version
  sku_name   = var.db_sku_name
  storage_mb = var.db_storage_mb

  administrator_login    = "kuruops_admin"
  administrator_password = local.db_master_password

  delegated_subnet_id = azurerm_subnet.db.id
  private_dns_zone_id = azurerm_private_dns_zone.db.id

  # Sample-appropriate, not production-appropriate -- no zone-redundant HA,
  # 7-day backups (the flexible server minimum). Bump for anything real.
  zone                   = "1"
  backup_retention_days  = 7

  depends_on = [azurerm_private_dns_zone_virtual_network_link.db]
}

resource "azurerm_postgresql_flexible_server_database" "kuruops" {
  name      = "kuruops"
  server_id = azurerm_postgresql_flexible_server.main.id
  charset   = "UTF8"
  collation = "en_US.utf8"
}
