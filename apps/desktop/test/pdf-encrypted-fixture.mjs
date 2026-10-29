import { createHash } from 'node:crypto';

// A real PDF Standard Security R2 fixture. Legacy MD5/RC4 is used only to author
// this synthetic password-protected test input, never to protect application data.
export function pdfEncryptedFixture() {
  const padding=Buffer.from('28bf4e5e4e758a4164004e56fffa01082e2e00b6d0683e802f0ca9fe6453697a','hex');
  const padded=value=>Buffer.concat([Buffer.from(value),padding]).subarray(0,32);
  const md5=value=>createHash('md5').update(value).digest();
  const rc4=(key,input)=>{
    const state=Array.from({length:256},(_,index)=>index);let j=0;
    for(let i=0;i<256;i++){j=(j+state[i]+key[i%key.length])&255;[state[i],state[j]]=[state[j],state[i]];}
    const output=Buffer.alloc(input.length);let i=0;j=0;
    for(let n=0;n<input.length;n++){i=(i+1)&255;j=(j+state[i])&255;[state[i],state[j]]=[state[j],state[i]];output[n]=input[n]^state[(state[i]+state[j])&255];}
    return output;
  };
  const identity=md5(Buffer.from('SparkX synthetic encrypted PDF'));
  const owner=rc4(md5(padded('synthetic-owner')).subarray(0,5),padded('synthetic-password'));
  const permissions=Buffer.alloc(4);permissions.writeInt32LE(-4);
  const key=md5(Buffer.concat([padded('synthetic-password'),owner,permissions,identity])).subarray(0,5);
  const user=rc4(key,padding);
  const objects=[
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> >>',
    `<< /Filter /Standard /V 1 /R 2 /Length 40 /O <${owner.toString('hex')}> /U <${user.toString('hex')}> /P -4 >>`,
  ];
  let pdf='%PDF-1.4\n';const offsets=[];
  for(let i=0;i<objects.length;i++){offsets.push(Buffer.byteLength(pdf));pdf+=`${i+1} 0 obj\n${objects[i]}\nendobj\n`;}
  const start=Buffer.byteLength(pdf);
  pdf+='xref\n0 5\n0000000000 65535 f \n'+offsets.map(offset=>String(offset).padStart(10,'0')+' 00000 n \n').join('');
  pdf+=`trailer\n<< /Size 5 /Root 1 0 R /Encrypt 4 0 R /ID [<${identity.toString('hex')}> <${identity.toString('hex')}>] >>\nstartxref\n${start}\n%%EOF`;
  return Buffer.from(pdf);
}
