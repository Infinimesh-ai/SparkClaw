"""Verify an owned Debian artifact: python3 SCRIPT PACKAGE.deb UNPACKED/sparkx."""
import json,os,re,subprocess,sys
from pathlib import Path
deb,exe=(Path(argument).resolve(strict=True) for argument in sys.argv[1:])
depends=subprocess.check_output(['dpkg-deb','-f',str(deb),'Depends'],text=True).strip()
roots=[]
for requirement in depends.split(','):
    for alternative in requirement.split('|'):
        name=re.match(r'\s*([a-z0-9+.-]+)',alternative)[1]
        result=subprocess.run(['dpkg-query','-W','-f=${Status}',name],capture_output=True,text=True)
        if result.returncode==0 and result.stdout=='install ok installed':roots.append(name);break
    else:raise AssertionError('Unsatisfied required package: '+requirement)
closure_text=subprocess.check_output(['apt-cache','depends','--recurse','--no-recommends','--no-suggests','--no-conflicts','--no-breaks','--no-replaces','--no-enhances',*roots],text=True)
closure={x.split(':')[0] for x in closure_text.splitlines() if x and not x.startswith(' ')}
ldd=subprocess.check_output(['ldd',str(exe)],text=True)
assert 'not found' not in ldd
rows=[]
for filename in re.findall(r'=> (/[^ ]+)',ldd):
    if Path(filename).is_relative_to(exe.parent):rows.append({'library':Path(filename).name,'bundled':True});continue
    owner=None
    for candidate in [filename,os.path.realpath(filename),filename.replace('/lib/','/usr/lib/',1)]:
        result=subprocess.run(['dpkg-query','-S',candidate],capture_output=True,text=True)
        if result.returncode==0:owner=result.stdout.split(': ')[0].split(':')[0];break
    assert owner and owner in closure,(filename,owner)
    rows.append({'library':Path(filename).name,'package':owner,'required_dependency_closure':True})
print(json.dumps({'architecture':'arm64','depends':depends,'all_resolved':True,'all_system_libraries_declared_transitively':True,'libraries':rows},indent=2))
