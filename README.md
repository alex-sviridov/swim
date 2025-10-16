Sll
WIM stands for ScaleWay Instance Manager
It's the system for automated provisioning of short-life VM Instances in scaleway cloud. 
Written in go 1.25
Part of the linuxlab project

One of three options for VM provisioning available:
- read json as the command argument and print provisioned machine details (one-shot run)
- connect to redis queue provided in parameters and post status into redis cache (service)