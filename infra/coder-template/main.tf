terraform {
  required_version = ">= 1.5.0"

  required_providers {
    coder = {
      source  = "coder/coder"
      version = "2.18.0"
    }
    incus = {
      source  = "lxc/incus"
      version = "1.1.1"
    }
  }
}

provider "incus" {}

data "coder_provisioner" "me" {}
data "coder_workspace" "me" {}
data "coder_workspace_owner" "me" {}

data "coder_parameter" "cpu" {
  name         = "cpu"
  display_name = "CPU"
  description  = "Número de CPU del workspace."
  type         = "number"
  default      = "2"
  mutable      = true

  validation {
    min = 1
    max = 8
  }
}

data "coder_parameter" "memory" {
  name         = "memory"
  display_name = "Memoria (GiB)"
  description  = "Memoria del workspace."
  type         = "number"
  default      = "4"
  mutable      = true

  validation {
    min = 1
    max = 32
  }
}

data "coder_parameter" "storage_pool" {
  name         = "storage_pool"
  display_name = "Pool de Incus"
  description  = "Pool para el sistema y el home persistente."
  type         = "string"
  default      = "default"
  mutable      = false
}

data "coder_parameter" "home_size" {
  name         = "home_size"
  display_name = "Home (GiB)"
  description  = "Tamaño del home persistente."
  type         = "number"
  default      = "20"
  mutable      = false

  validation {
    min = 5
    max = 500
  }
}

locals {
  workspace_user = "coder"
  instance_name  = "coder-${data.coder_workspace.me.id}"
  storage_pool   = data.coder_parameter.storage_pool.value
}

resource "coder_agent" "main" {
  count = data.coder_workspace.me.start_count
  arch  = data.coder_provisioner.me.arch
  os    = "linux"

  display_apps {
    vscode       = true
    web_terminal = true
    ssh_helper   = true
  }

  metadata {
    display_name = "CPU"
    key          = "cpu_usage"
    script       = "coder stat cpu"
    interval     = 10
    timeout      = 1
    order        = 1
  }

  metadata {
    display_name = "RAM"
    key          = "ram_usage"
    script       = "coder stat mem"
    interval     = 10
    timeout      = 1
    order        = 2
  }
}

resource "incus_storage_volume" "home" {
  name        = "coder-${data.coder_workspace.me.id}-home"
  pool        = local.storage_pool
  description = "Home persistente de ${data.coder_workspace_owner.me.name}/${data.coder_workspace.me.name}"
  config = {
    size = "${data.coder_parameter.home_size.value}GiB"
  }
}

resource "incus_instance" "workspace" {
  count     = data.coder_workspace.me.start_count
  name      = local.instance_name
  type      = "container"
  image     = "images:ubuntu/24.04/cloud"
  profiles  = ["default"]
  ephemeral = false

  config = {
    "boot.autostart" = "true"
    "limits.cpu"     = tostring(data.coder_parameter.cpu.value)
    "limits.memory"  = "${data.coder_parameter.memory.value}GiB"

    "cloud-init.user-data" = <<-CLOUD_CONFIG
      #cloud-config
      hostname: ${local.instance_name}
      package_update: true
      packages:
        - ca-certificates
        - curl
        - git
      users:
        - name: ${local.workspace_user}
          uid: 1001
          groups: sudo
          shell: /bin/bash
          sudo: ["ALL=(ALL) NOPASSWD:ALL"]
      write_files:
        - path: /opt/coder/init.sh
          owner: root:root
          permissions: "0755"
          encoding: b64
          content: ${base64encode(coder_agent.main[0].init_script)}
        - path: /etc/coder-agent.env
          owner: root:root
          permissions: "0600"
          content: |
            CODER_AGENT_TOKEN=${coder_agent.main[0].token}
        - path: /etc/systemd/system/coder-agent.service
          owner: root:root
          permissions: "0644"
          content: |
            [Unit]
            Description=Coder Agent
            After=network-online.target
            Wants=network-online.target

            [Service]
            User=${local.workspace_user}
            WorkingDirectory=/home/${local.workspace_user}
            EnvironmentFile=/etc/coder-agent.env
            ExecStart=/opt/coder/init.sh
            Restart=always
            RestartSec=5
            TimeoutStopSec=90
            KillMode=process

            [Install]
            WantedBy=multi-user.target
      runcmd:
        - chown -R ${local.workspace_user}:${local.workspace_user} /home/${local.workspace_user}
        - systemctl daemon-reload
        - systemctl enable --now coder-agent.service
    CLOUD_CONFIG
  }

  device {
    name = "root"
    type = "disk"
    properties = {
      path = "/"
      pool = local.storage_pool
    }
  }

  device {
    name = "home"
    type = "disk"
    properties = {
      path   = "/home/${local.workspace_user}"
      pool   = local.storage_pool
      source = incus_storage_volume.home.name
    }
  }

  wait_for {
    type = "cloud-init"
  }
}

resource "coder_metadata" "workspace" {
  count       = data.coder_workspace.me.start_count
  resource_id = coder_agent.main[0].id

  item {
    key   = "instance"
    value = incus_instance.workspace[0].name
  }

  item {
    key   = "image"
    value = "Ubuntu 24.04 LTS"
  }

  item {
    key   = "cpu"
    value = "${data.coder_parameter.cpu.value} vCPU"
  }

  item {
    key   = "memory"
    value = "${data.coder_parameter.memory.value} GiB"
  }

  item {
    key   = "home"
    value = "${data.coder_parameter.home_size.value} GiB persistentes"
  }
}
