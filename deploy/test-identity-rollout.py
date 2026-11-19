#!/usr/bin/env python3
"""Local launcher and deployment checks; every remote/build command is replaced."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

project = Path(__file__).resolve().parents[1]

def executable(path, source):
    path.write_text(source)
    path.chmod(0o755)

def run(command, env, success=True):
    result = subprocess.run(command, env=env, capture_output=True, text=True)
    assert (result.returncode == 0) == success, 'unexpected local rollout check status'
    assert 'private-fixture-password' not in result.stdout + result.stderr, 'credential appeared in output'
    return result

with tempfile.TemporaryDirectory(prefix='moneybag-rollout-') as folder:
    root = Path(folder)
    shutil.copyfile(project / 'deploy/run', root / 'run')
    original = b'MONEYBAG_DATABASE_URL=postgres://owned-fixture/test\nMONEYBAG_SECRET_KEY=keep-feed-key\nMONEYBAG_ID_ENABLED=false\n'
    (root / 'moneybag.env').write_bytes(original)
    (root / 'moneybag.env').chmod(0o600)
    (root / 'current').mkdir()
    executable(root / 'current/moneybag', '''#!/usr/bin/env python3
import os
assert os.environ['MONEYBAG_DATABASE_URL']=='postgres://owned-fixture/test'
assert os.environ['MONEYBAG_SECRET_KEY']=='keep-feed-key'
assert os.environ['MONEYBAG_ID_ENABLED']==os.environ['MONEYBAG_TEST_ENABLED']
if 'MONEYBAG_TEST_ACCESS_EXPECTED' in os.environ:assert os.environ['MONEYBAG_ID_ACCESS_ENABLED']==os.environ['MONEYBAG_TEST_ACCESS_EXPECTED']
if os.environ['MONEYBAG_ID_ENABLED']=='true':
 assert os.environ['MONEYBAG_ID_CLIENT_SECRET']=='$(touch "$MONEYBAG_TEST_INJECTION")'
open(os.environ['MONEYBAG_TEST_EXECUTED'],'w').close()
''')
    executed, injection = root / 'executed', root / 'injected'
    env = {**os.environ, 'MONEYBAG_TEST_EXECUTED': str(executed), 'MONEYBAG_TEST_INJECTION': str(injection), 'MONEYBAG_TEST_ENABLED': 'false'}
    run(['bash', str(root / 'run')], env)
    assert executed.exists()
    identity = root / 'identity.env'
    identity.write_text('MONEYBAG_ID_ENABLED=true\nMONEYBAG_ID_CLIENT_ID=fixture-client\nMONEYBAG_ID_CLIENT_SECRET=$(touch "$MONEYBAG_TEST_INJECTION")\n')
    identity.chmod(0o600)
    env['MONEYBAG_TEST_ENABLED'] = 'true'
    run(['bash', str(root / 'run')], env)
    assert not injection.exists()
    executed.unlink()
    identity.chmod(0o644)
    run(['bash', str(root / 'run')], env, False)
    identity.chmod(0o600)
    for content in ['MONEYBAG_DATABASE_URL=must-not-override\n', 'MONEYBAG_SECRET_KEY=must-not-override\n', 'invalid assignment\n']:
        identity.write_text(content)
        run(['bash', str(root / 'run')], env, False)
    identity.unlink()
    identity.symlink_to(root / 'moneybag.env')
    run(['bash', str(root / 'run')], env, False)
    assert not executed.exists()
    assert (root / 'moneybag.env').read_bytes() == original
    assert (root / 'moneybag.env').stat().st_mode & 0o777 == 0o600

    identity.unlink()
    mode_file=root/'current/access.flag'
    mode_file.write_text('true\n');mode_file.chmod(0o600)
    env.update(MONEYBAG_TEST_ENABLED='false',MONEYBAG_TEST_ACCESS_EXPECTED='true',MONEYBAG_ID_ACCESS_ENABLED='false')
    run(['bash',str(root/'run')],env)
    mode_file.chmod(0o644);run(['bash',str(root/'run')],env,False);mode_file.chmod(0o600)
    mode_file.write_text('invalid\n');run(['bash',str(root/'run')],env,False)
    mode_file.unlink();mode_file.symlink_to(root/'moneybag.env');run(['bash',str(root/'run')],env,False)
    mode_file.unlink()
    tools = root / 'tools'
    tools.mkdir()
    executable(tools / 'git', '''#!/usr/bin/env bash
case "$*" in
 'symbolic-ref --quiet --short HEAD') echo "${MONEYBAG_TEST_BRANCH:-dev}" ;;
 'status --porcelain') printf '%s' "${MONEYBAG_TEST_DIRTY:-}" ;;
 'rev-parse --short=12 HEAD') echo aabbccddeeff ;;
 *) exit 98 ;;
esac
''')
    calls = root / 'remote-called'
    for tool in ['ssh', 'scp', 'make', 'npm', 'go']:
        executable(tools / tool, '#!/usr/bin/env bash\n: >"$MONEYBAG_TEST_CALLS"\nexit 99\n')
    env = {k: v for k, v in os.environ.items() if not k.startswith('MONEYBAG_SMOKE_')}
    env.update(PATH=str(tools) + ':' + os.environ['PATH'], MONEYBAG_TEST_CALLS=str(calls))
    deploy = ['bash', str(project / 'deploy/deploy.sh')]
    run(deploy, env, False)
    password = root / 'password'
    password.write_text('private-fixture-password\n')
    password.chmod(0o600)
    env['MONEYBAG_SMOKE_PASSWORD_FILE'] = str(password)
    run(deploy, {**env, 'MONEYBAG_SMOKE_EMAIL': 'owner@example.test'}, False)
    run(deploy, {**env, 'MONEYBAG_SMOKE_ORIGIN': 'http://production.test'}, False)
    run(deploy, {**env, 'MONEYBAG_TEST_BRANCH': 'feature'}, False)
    run(deploy, {**env, 'MONEYBAG_TEST_DIRTY': ' M file'}, False)
    password.chmod(0o644)
    run(deploy, env, False)
    run(deploy + ['--dry-run'], env)
    assert not calls.exists(), 'preflight performed a remote/build action'

    executable(tools / 'node', '''#!/usr/bin/env bash
[[ "${2:-}" == --check-config ]] && exit 0
: >"$MONEYBAG_TEST_SMOKE"
exit "${MONEYBAG_TEST_SMOKE_STATUS:-78}"
''')
    wrapper = root / 'activation.sh'
    wrapper.write_text('''#!/usr/bin/env bash
set -Eeuo pipefail
source "$MONEYBAG_TEST_DEPLOY"
verify_local() { return "${MONEYBAG_TEST_VERIFY_STATUS:-0}"; }
capture_previous() { previous=releases/aabbccddeeff-20260101000000; }
upload_release() { :; }
activate() { return "${MONEYBAG_TEST_ACTIVATE_STATUS:-0}"; }
rollback() { printf 'rollback\\n' >>"$MONEYBAG_TEST_ROLLBACKS"; return "${MONEYBAG_TEST_ROLLBACK_STATUS:-0}"; }
main
''')
    rollbacks, smoke = root / 'rollbacks', root / 'smoke'
    env.update(MONEYBAG_TEST_DEPLOY=str(project / 'deploy/deploy.sh'), MONEYBAG_TEST_ROLLBACKS=str(rollbacks), MONEYBAG_TEST_SMOKE=str(smoke))
    result = run(['bash', str(wrapper)], env, False)
    assert result.returncode == 78 and rollbacks.read_text() == 'rollback\n' and smoke.exists()
    rollbacks.unlink()
    smoke.unlink()
    result = run(['bash', str(wrapper)], {**env, 'MONEYBAG_TEST_ACTIVATE_STATUS': '17'}, False)
    assert result.returncode == 17 and rollbacks.read_text() == 'rollback\n' and not smoke.exists()
    rollbacks.unlink()
    result = run(['bash', str(wrapper)], {**env, 'MONEYBAG_TEST_ROLLBACK_STATUS': '3'}, False)
    assert result.returncode == 78 and rollbacks.read_text() == 'rollback\n'
    rollbacks.unlink()
    smoke.unlink()
    result = run(['bash', str(wrapper)], {**env, 'MONEYBAG_TEST_VERIFY_STATUS': '18'}, False)
    assert result.returncode == 18 and not rollbacks.exists() and not smoke.exists()
    run(['bash', str(wrapper)], {**env, 'MONEYBAG_TEST_SMOKE_STATUS': '0'})
    assert not rollbacks.exists() and smoke.exists() and not calls.exists()
print('Microapps ID/access launcher, deployment preflight and mode-preserving activation/smoke rollback checks passed')
