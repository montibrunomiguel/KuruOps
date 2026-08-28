<p align="right"><a href="README.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# KuruOps na AWS (EKS)

Provisiona a infraestrutura de nuvem que o KuruOps precisa: uma VPC, um
cluster EKS com um node group gerenciado, uma instância RDS Postgres 16,
quatro repositórios ECR (api/ingest/worker/frontend) e — opcionalmente — um
bucket S3 para upload de anexos. Isso **não** aplica os manifests de
`deploy/k8s/`, não roda `db/migrations/`, nem configura ingress/TLS/
autoscaling — veja a seção "Próximos passos" deste arquivo e a nota de
escopo do próprio `deploy/k8s/README.md` para entender por que isso continua
manual.

## Pré-requisitos

- Terraform >= 1.5
- AWS CLI configurada com credenciais que possam criar VPCs/EKS/RDS/ECR/IAM
- `kubectl`, `docker` e `psql` (ou `migrate`) na sua máquina

## 1. Provisionar a infraestrutura

```bash
cd deploy/aws
cp terraform.tfvars.example terraform.tfvars
# edite terraform.tfvars — no mínimo escolha uma região/AZs onde você realmente tenha quota

terraform init
terraform plan
terraform apply
```

Isso leva de 10 a 15 minutos (a criação do cluster EKS é a parte mais lenta).

## 2. Apontar o kubectl para o novo cluster

```bash
$(terraform output -raw configure_kubectl)
kubectl get nodes   # deve mostrar node_desired_size nós Ready
```

## 3. Fazer build e push das quatro imagens

```bash
$(terraform output -raw ecr_login_command)

cd ../..   # raiz do repositório
REGISTRY=$(cd deploy/aws && terraform output -json ecr_repository_urls)
for target in api ingest worker; do
  repo=$(echo "$REGISTRY" | jq -r ".$target")
  docker build --target "$target" -t "$repo:latest" -f backend/Dockerfile .
  docker push "$repo:latest"
done
repo=$(echo "$REGISTRY" | jq -r ".frontend")
docker build -t "$repo:latest" ./frontend
docker push "$repo:latest"
```

## 4. Criar as roles do banco de dados

`db/init/kuruops_app_role.sql` e `kuruops_worker_role.sql` configuram as
duas roles de privilégio mínimo com as quais a aplicação realmente se
conecta (RLS só se aplica a uma role sem `BYPASSRLS`/posse da tabela — veja
`db/README.md`). Execute-as uma vez contra a instância RDS usando as
credenciais master:

```bash
cd deploy/aws
DB_HOST=$(terraform output -raw db_endpoint)
DB_USER=$(terraform output -raw db_master_username)
DB_PASS=$(terraform output -raw db_master_password)

psql "postgres://$DB_USER:$DB_PASS@$DB_HOST/kuruops?sslmode=require" \
  -v app_password='CHANGE-ME-APP-PASSWORD' \
  -f ../../db/init/kuruops_app_role.sql
psql "postgres://$DB_USER:$DB_PASS@$DB_HOST/kuruops?sslmode=require" \
  -v worker_password='CHANGE-ME-WORKER-PASSWORD' \
  -f ../../db/init/kuruops_worker_role.sql
```

## 5. Preencher o Secret e o ConfigMap de `deploy/k8s/`

Copie `deploy/k8s/01-secret.example.yaml` e preencha:
- `DATABASE_URL_APP` / `DATABASE_URL_WORKER` — o `db_endpoint` acima + as
  senhas que você acabou de definir para `kuruops_app`/`kuruops_worker`
- `DATABASE_URL_MIGRATE` — o `db_endpoint` acima + as credenciais master do
  RDS (outputs `db_master_username`/`db_master_password`)
- `SECRETS_ENCRYPTION_KEY` — `openssl rand -base64 32`
- `JWT_PRIVATE_KEY`/`JWT_PUBLIC_KEY` — gere conforme o próprio comentário
  daquele arquivo

Atualize o `APP_BASE_URL` de `deploy/k8s/02-configmap.yaml` para onde quer
que você vá expor o Service do frontend (veja o passo 7).

## 6. Atualizar as referências de imagem e aplicar

Atualize o `image:` em `deploy/k8s/04-api.yaml`, `05-ingest.yaml`,
`06-worker.yaml`, `07-frontend.yaml` e o initContainer `copy-migrations` de
`03-migration-job.yaml` para as URLs do ECR do passo 3, então siga a ordem
de aplicação de `deploy/k8s/README.md`.

## 7. Acessar a aplicação de fora do cluster

O Service do `frontend` é `ClusterIP` de propósito — esta stack não instala
um controlador de ingress nem o AWS Load Balancer Controller. Forma mais
rápida de experimentar:

```bash
kubectl -n kuruops port-forward svc/frontend 8080:8080
```

Para algo real, instale o AWS Load Balancer Controller e adicione um
`Ingress` (ou troque o Service do `frontend` para `type: LoadBalancer` para
um ALB/NLB simples e rápido, sem necessidade de roteamento por path já que o
ingest — se você o expuser separadamente para webhooks de entrada — tem seu
próprio Service).

## Desmontando tudo

```bash
cd deploy/aws
terraform destroy
```

`skip_final_snapshot = true` na instância RDS significa que isso não deixa
um snapshot cobrado para trás — não confie nisso para nada com dados reais.

## O que isso deliberadamente não faz

Mesmo corte de escopo de `deploy/k8s/README.md`: nenhum controlador de
ingress, nenhum autoscaling, nenhuma integração IRSA/OIDC (nada aqui precisa
de uma identidade AWS ambiente — a integração de uploads no S3 usa uma
access key estática via Settings, veja o comentário de `s3.tf`), nenhum RDS
Multi-AZ, um único NAT gateway. Isso é uma amostra para construir uma
implantação de verdade a partir dela, não uma arquitetura de referência para
produção.
