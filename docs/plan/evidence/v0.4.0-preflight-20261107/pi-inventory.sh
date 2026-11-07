#!/bin/sh
set -eu
printf 'utc='; date -u +%FT%TZ
uname -a
getconf LONG_BIT
for path in /proc/device-tree/model /etc/os-release /proc/meminfo /proc/swaps /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor; do
  printf '\n[%s]\n' "$path"
  if test -r "$path"; then tr '\000' '\n' < "$path"; else printf 'null\n'; fi
done
if command -v lsblk >/dev/null; then lsblk -o NAME,MODEL,TRAN,SIZE,FSTYPE,MOUNTPOINTS; else printf 'lsblk=null\n'; fi
if command -v vcgencmd >/dev/null; then
  vcgencmd version || printf 'firmware=null\n'
  vcgencmd get_throttled || printf 'throttling=null\n'
  vcgencmd measure_temp || printf 'temperature=null\n'
else
  printf 'firmware=null\nthrottling=null\ntemperature=null\n'
fi
df -PT .
