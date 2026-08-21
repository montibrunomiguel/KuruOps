<p align="right"><a href="README.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# ArgusOps no Azure (AKS)

Provisiona a infraestrutura de nuvem que o ArgusOps precisa: um resource
group + VNet, um cluster AKS, um Azure Database for PostgreSQL Flexible
Server integrado à VNet (somente acesso privado) e um Azure Container
Registry contendo as quatro imagens. Isso **não** aplica os manifests de
`deploy/k8s/`, não roda `db/migrations/`, nem configura ingress/TLS/
autoscaling — mesmo corte de escopo das stacks de AWS/GCP, veja a nota de
escopo de `deploy/k8s/README.md`.

## Pré-requisitos

- Terraform >= 1.5
- CLI `az` autenticada (`az login`), com uma subscription selecionada
- `kubectl`, `docker` e `psql` na sua máquina

## Nenhum bucket de armazenamento de objetos aqui

Diferente das stacks de AWS/GCP, esta não provisiona um bucket de blob
storage: `internal/blobstore` (a funcionalidade Settings -> Storage
Integration da aplicação) hoje só tem backends S3 e GCS, nenhum backend de
Azure Blob Storage. Os uploads permanecem no PVC de disco local já presente
em `deploy/k8s/04-api.yaml`, o que funciona bem com `replicas: 1` (o
padrão). Se você precisar que os anexos sobrevivam entre múltiplas réplicas
de `api` no Azure, ou troque a StorageClass daquele PVC para Azure Files
(`ReadWriteMany`), ou aponte a Storage Integration da aplicação para um
endpoint compatível com S3 (por exemplo, um MinIO auto-hospedado), caso
tenha um.

## 1. Provisionar a infraestrutura

```bash
cd deploy/azure
cp terraform.tfvars.example terraform.tfvars
# edite terraform.tfvars

terraform init
terraform plan
terraform apply
```

## 2. Apontar o kubectl para o novo cluster

```bash
$(terraform output -raw configure_kubectl)
kubectl get nodes
```

## 3. Fazer build e push das quatro imagens

```bash
$(terraform output -raw acr_login_command)

cd ../..   # raiz do repositório
REGISTRY=$(cd deploy/azure && terraform output -raw acr_login_server)
for target in api ingest worker; do
  docker build --target "$target" -t "$REGISTRY/argusops/$target:latest" -f backend/Dockerfile .
  docker push "$REGISTRY/argusops/$target:latest"
done
docker build -t "$REGISTRY/argusops/frontend:latest" ./frontend
docker push "$REGISTRY/argusops/frontend:latest"
```

## 4. Criar as roles do banco de dados

O Flexible Server não tem endpoint público — execute os dois scripts
`db/init/*.sql` a partir de algo que já esteja dentro da VNet (um pod do
AKS, uma VM jumpbox, o Azure Cloud Shell com peering de VNet). Por exemplo,
a partir de um pod descartável:

```bash
kubectl run psql-tmp --rm -it --restart=Never --image=postgres:16-alpine -- \
  psql "postgres://argusops_admin:$(cd deploy/azure && terraform output -raw db_master_password)@$(cd deploy/azure && terraform output -raw db_host)/argusops?sslmode=require"
```

depois cole o conteúdo de `db/init/argusops_app_role.sql` e
`argusops_worker_role.sql` (substituindo senhas reais nas variáveis psql
`:app_password`/`:worker_password`, ou execute o corpo de cada script
diretamente).

## 5. Preencher o Secret e o ConfigMap de `deploy/k8s/`

Copie `deploy/k8s/01-secret.example.yaml` e preencha:
- `DATABASE_URL_APP` / `DATABASE_URL_WORKER` — o output `db_host` + as
  senhas que você acabou de definir para `argusops_app`/`argusops_worker`
- `DATABASE_URL_MIGRATE` — o output `db_host` + as credenciais de
  `argusops_admin` (outputs `db_master_username`/`db_master_password`)
- `SECRETS_ENCRYPTION_KEY` — `openssl rand -base64 32`
- `JWT_PRIVATE_KEY`/`JWT_PUBLIC_KEY` — gere conforme o próprio comentário
  daquele arquivo

Atualize o `APP_BASE_URL` de `deploy/k8s/02-configmap.yaml` para onde você
vai expor o Service do frontend (veja o passo 7).

## 6. Atualizar as referências de imagem e aplicar

Atualize o `image:` em `deploy/k8s/04-api.yaml`, `05-ingest.yaml`,
`06-worker.yaml`, `07-frontend.yaml` e o initContainer `copy-migrations` de
`03-migration-job.yaml` para `$REGISTRY/argusops/<name>:latest` do passo 3,
então siga a ordem de aplicação de `deploy/k8s/README.md`.

## 7. Acessar a aplicação de fora do cluster

O Service do `frontend` é `ClusterIP` de propósito — não há controlador de
ingress aqui. Forma mais rápida de experimentar:

```bash
kubectl -n argusops port-forward svc/frontend 8080:8080
```

Para algo real, instale o add-on de application routing gerenciado pelo AKS
(ou seu próprio ingress-nginx) ou troque o Service do `frontend` para
`type: LoadBalancer` para obter um IP externo rapidamente.

## Desmontando tudo

```bash
cd deploy/azure
terraform destroy
```

## O que isso deliberadamente não faz

Mesmo corte de escopo das stacks de AWS/GCP: nenhum ingress, nenhum
autoscaling, nenhuma integração de workload identity (nada aqui precisa de
uma identidade Azure ambiente — o pull do ACR é concedido diretamente à
identidade kubelet do AKS, o padrão convencional), nenhum Postgres com HA
zone-redundant, um único node pool. Uma amostra para construir uma
implantação de verdade a partir dela, não uma arquitetura de referência para
produção.
