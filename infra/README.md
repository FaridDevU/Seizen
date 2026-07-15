# Infraestructura

La base de Seizen es local y no necesita infraestructura. Este directorio solo
contiene la plantilla de workspaces Coder sobre Incus.

Requisitos: Terraform >= 1.5, Incus inicializado con un perfil `default` y el
pool indicado, y la CLI de Coder autenticada.

```powershell
cd infra/coder-template
terraform init
coder templates push seizen-incus --directory .
```

No ejecute `terraform apply` sobre la plantilla: Coder aplica cada workspace.
Al detenerlo se eliminan el contenedor y el agente; el volumen de `/home`
permanece y se conecta a la nueva instancia en el siguiente arranque.
El provider `incus {}` se ejecuta donde corre el provisioner de Coder, no en el
escritorio del usuario. Ese proceso debe ver el socket local de Incus o tener un
remote HTTPS confiable configurado mediante certificados/variables `INCUS_*`;
nunca incluya tokens de confianza en la plantilla.
