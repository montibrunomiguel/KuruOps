output "cluster_name" {
  value = azurerm_kubernetes_cluster.main.name
}

output "resource_group_name" {
  value = azurerm_resource_group.main.name
}

output "configure_kubectl" {
  description = "Run this to point kubectl/deploy/k8s at the new cluster."
  value       = "az aks get-credentials --resource-group ${azurerm_resource_group.main.name} --name ${azurerm_kubernetes_cluster.main.name}"
}

output "db_host" {
  description = "Reachable only from inside the VNet (AKS nodes) -- goes into deploy/k8s/01-secret.example.yaml's DATABASE_URL_* fields."
  value       = azurerm_postgresql_flexible_server.main.fqdn
}

output "db_master_username" {
  value = "kuruops_admin"
}

output "db_master_password" {
  value     = local.db_master_password
  sensitive = true
}

output "acr_login_server" {
  description = "docker push target -- append /api, /ingest, /worker, /frontend."
  value       = azurerm_container_registry.main.login_server
}

output "acr_login_command" {
  value = "az acr login --name ${azurerm_container_registry.main.name}"
}
