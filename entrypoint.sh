#!/bin/bash
set -e

# Load environment overrides from persistent volume
set -o allexport
if test -f "/data/jottad/jottad.env"; then
  source /data/jottad/jottad.env
fi
set +o allexport

# Support Docker secrets
if test -f "/run/secrets/jotta_token"; then
  JOTTA_TOKEN=$(cat /run/secrets/jotta_token)
fi

# Set timezone
if [ -f "/usr/share/zoneinfo/$LOCALTIME" ]; then
  rm -f /etc/localtime
  ln -s /usr/share/zoneinfo/$LOCALTIME /etc/localtime
fi

# Allow running bash for debugging
if [ $# -eq 1 ] && [ "$@" = "bash" ]; then
  exec "$@"
fi

# Set up persistent data directories
mkdir -p /data/jottad
ln -sfn /data/jottad /root/.jottad
mkdir -p /data/jotta-cli
mkdir -p /root/.config
ln -sfn /data/jotta-cli /root/.config/jotta-cli

# Start the jottad daemon
/usr/bin/run_jottad &

sleep 5

# Disable exit-on-error for status checking
set +e

echo -n "Waiting for jottad to start (timeout: ${STARTUP_TIMEOUT}s). "

while :; do
  timeout 1 jotta-cli status >/dev/null 2>&1
  R=$?

  if [ $R -eq 0 ]; then
    echo "Jottad started."
    break
  fi

  if [ $R -ne 0 ]; then
    echo "Could not start jottad. Checking why."

    STATUS_OUTPUT=$(timeout 1 jotta-cli status 2>&1)

    if [[ "$STATUS_OUTPUT" =~ "Found remote device that matches this machine" ]]; then
      echo "Found matching device name, re-using."
      /usr/bin/expect -c "
      set timeout 1
      spawn jotta-cli status
      expect \"Do you want to re-use this device? (yes/no): \" {send \"yes\n\"}
      expect eof
      "

    elif [[ "$STATUS_OUTPUT" =~ "Error: The session has been revoked." ]]; then
      echo "Session expired. Logging out and back in."
      /usr/bin/expect -c "
      set timeout 20
      spawn jotta-cli logout
      expect \"Backup will stop. Continue?(y/n): \" {send \"y\n\"}
      expect eof
      "

      /usr/bin/expect -c "
      set timeout 20
      spawn jotta-cli login
      expect \"accept license (yes/no): \" {send \"yes\n\"}
      expect \"Personal login token: \" {send \"$JOTTA_TOKEN\n\"}
      expect \"Do you want to re-use this device? (yes/no): \" {send \"yes\n\"}
      expect eof
      "

    elif [[ "$STATUS_OUTPUT" =~ "Not logged in" ]]; then
      echo "First time login."

      /usr/bin/expect -c "
        set timeout 20
        spawn jotta-cli login
        expect \"accept license (yes/no): \" {send \"yes\n\"}
        expect \"Personal login token: \" {send \"$JOTTA_TOKEN\n\"}
        expect {
          eof {
            exit 1
          }
          \"Devicename*: \" {
            send \"$JOTTA_DEVICE\n\"
            expect eof
          }
          \"Do you want to re-use this device? (yes/no):\" {
            send \"yes\n\"
            expect eof
          }
        }
      "
      R=$?
      if [ $R -ne 0 ]; then
        echo "Login failed."
        exit 1
      fi
    fi
  fi

  if [ "$STARTUP_TIMEOUT" -le 0 ]; then
    echo "Startup timeout reached."
    echo "ERROR: Unable to determine why jottad cannot start:"
    jotta-cli status
    exit 1
  fi

  STARTUP_TIMEOUT=$((STARTUP_TIMEOUT - 1))
  echo -n ".$STARTUP_TIMEOUT."
  sleep 1
done

# Add backup directories
echo "Adding backup directories."
for dir in /backup/*; do
  if [ -d "${dir}" ]; then
    set +e
    jotta-cli add "${dir}"
    set -e
  fi
done

# Add sync directory (jotta-cli supports only one sync root)
if [ -d "/sync" ] && [ "$(ls -A /sync 2>/dev/null)" ]; then
  echo "Adding sync directory."
  set +e
  jotta-cli sync setup --root /sync
  set -e
fi

# Load ignore file if present
if [ -f /config/ignorefile ]; then
  echo "Loading ignore file."
  jotta-cli ignores set /config/ignorefile
fi

# Set scan interval
echo "Setting scan interval to ${JOTTA_SCANINTERVAL}."
jotta-cli config set scaninterval $JOTTA_SCANINTERVAL

# Tail logs in background
jotta-cli tail &

# Health check loop
R=0
while [ $R -eq 0 ]; do
  sleep 15
  jotta-cli status >/dev/null 2>&1
  R=$?
done

echo "Jottad exited unexpectedly:"
jotta-cli status
exit 1
