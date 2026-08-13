# One registry holding all four images (ACR's own convention, same as
# Artifact Registry's: one registry, multiple repositories inside it --
# .../argusops/api, .../argusops/ingest, etc.).
#
# ACR pull access is granted directly to AKS's auto-created kubelet
# identity -- the standard "az aks create --attach-acr" pattern -- not a
# separate workload identity, since nothing else in this stack needs one.

resource "azurerm_container_registry" "main" {
  name                = replace("${var.name}${var.environment}acr", "-", "")
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  sku                 = "Standard"
  admin_enabled       = false
}

resource "azurerm_role_assignment" "aks_acr_pull" {
  scope                = azurerm_container_registry.main.id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_kubernetes_cluster.main.kubelet_identity[0].object_id
}
