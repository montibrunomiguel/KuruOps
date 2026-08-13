# AKS cluster, system-assigned identity (no separate user-assigned identity
# / workload identity wiring -- nothing in deploy/k8s/ needs an ambient
# Azure identity; see acr.tf's comment on why ACR pull is granted to the
# kubelet identity instead).

resource "azurerm_kubernetes_cluster" "main" {
  name                = "${var.name}-${var.environment}"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  dns_prefix          = "${var.name}-${var.environment}"
  kubernetes_version  = var.kubernetes_version

  default_node_pool {
    name           = "default"
    node_count     = var.node_count
    vm_size        = var.node_vm_size
    vnet_subnet_id = azurerm_subnet.aks.id
  }

  identity {
    type = "SystemAssigned"
  }

  network_profile {
    network_plugin = "azure"
  }
}
