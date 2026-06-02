# SSM bastion for connecting a local DB client (Navicat, psql, ...) to the private
# RDS instance. There is no inbound port and no SSH key: access is gated entirely by
# IAM via Session Manager, and every session is logged to CloudTrail.
#
# Designed for a start/stop workflow — keep the instance stopped, start it only while
# tunnelling. Stopped: ~$0.64/mo for the EBS root volume; the auto-assigned public IP
# is released on stop so it isn't billed. Running: ~$0.01/hr (t4g.nano + public IPv4).
# See the `bastion_tunnel_steps` output for the exact commands.

# AL2023 ships the SSM agent preinstalled, so the instance self-registers as a managed
# node with no user-data. arm64 image to match the t4g.nano instance type.
data "aws_ssm_parameter" "al2023_arm64" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64"
}

# No inbound. Egress-all so the agent can reach the SSM endpoints over the IGW (there
# is no NAT, which is why the bastion lives in a public subnet with a public IP).
resource "aws_security_group" "bastion" {
  name        = "${var.app_name_prefix}-bastion"
  description = "SSM bastion for DB tunnelling (no inbound)"
  vpc_id      = aws_vpc.main.id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-bastion" }
}

resource "aws_iam_role" "bastion" {
  name = "${var.app_name_prefix}-bastion"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
  tags = { Name = "${var.app_name_prefix}-bastion" }
}

# AmazonSSMManagedInstanceCore is all Session Manager + port forwarding needs.
resource "aws_iam_role_policy_attachment" "bastion_ssm" {
  role       = aws_iam_role.bastion.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "bastion" {
  name = "${var.app_name_prefix}-bastion"
  role = aws_iam_role.bastion.name
}

resource "aws_instance" "bastion" {
  ami                         = data.aws_ssm_parameter.al2023_arm64.value
  instance_type               = "t4g.nano"
  subnet_id                   = aws_subnet.public[0].id
  vpc_security_group_ids      = [aws_security_group.bastion.id]
  iam_instance_profile        = aws_iam_instance_profile.bastion.name
  associate_public_ip_address = true

  # Stopping/starting the instance out-of-band (the intended workflow) is not drift:
  # the AMI ID only matters at create time, so ignore it to avoid replacement when the
  # AL2023 SSM parameter advances to a newer image.
  lifecycle {
    ignore_changes = [ami]
  }

  tags = { Name = "${var.app_name_prefix}-bastion" }
}
