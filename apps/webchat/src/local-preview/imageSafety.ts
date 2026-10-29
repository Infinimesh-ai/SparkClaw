export const MAX_IMAGE_BYTES = 16 * 1024 * 1024;
export const MAX_IMAGE_PIXELS = 16_000_000;
export function imageType(bytes: Uint8Array) {
  const b = bytes; const ascii = (start: number, length: number) => String.fromCharCode(...b.slice(start,start+length));
  const u16 = (at: number) => b[at] * 256 + b[at+1]; const u32 = (at: number) => b[at]*2**24+b[at+1]*2**16+b[at+2]*256+b[at+3];
  let mime = ''; let width = 0; let height = 0;
  if (b.length >= 24 && b[0] === 137 && ascii(1,7) === 'PNG\r\n\x1a\n') { mime='image/png';width=u32(16);height=u32(20);let at=8;while(at+12<=b.length){const length=u32(at);if(ascii(at+4,4)==='acTL')throw new Error('Animated images are unavailable');at+=length+12;} }
  else if (b.length >= 10 && ['GIF87a','GIF89a'].includes(ascii(0,6))) { mime='image/gif';width=b[6]+256*b[7];height=b[8]+256*b[9]; let at=13+((b[10]&128)?3*2**((b[10]&7)+1):0),frames=0;while(at<b.length){const tag=b[at++];if(tag===59)break;if(tag===44){if(++frames>1)throw new Error('Animated images are unavailable');if(at+9>b.length)break;const flags=b[at+8];at+=9;if(flags&128)at+=3*2**((flags&7)+1);at++;}else if(tag===33){at++;}else break;while(at<b.length){const size=b[at++];if(!size)break;at+=size;}} }
  else if (b[0] === 255 && b[1] === 216) {
    mime='image/jpeg'; let offset=2;
    while (offset+9 < b.length && offset < 256*1024) { if(b[offset] !== 255) break; const marker=b[offset+1]; if(marker===216 || marker===217) { offset+=2;continue; } const size=u16(offset+2); if(size < 2) break;
      if ([192,193,194,195,197,198,199,201,202,203,205,206,207].includes(marker)) { height=u16(offset+5);width=u16(offset+7);break; } offset+=size+2;
    }
  } else if (b.length >= 30 && ascii(0,4)==='RIFF' && ascii(8,4)==='WEBP') {
    mime='image/webp'; const type=ascii(12,4);
    if(type==='VP8X' && (b[20]&2))throw new Error('Animated images are unavailable');
    if(type==='VP8X') {width=1+b[24]+256*b[25]+65536*b[26];height=1+b[27]+256*b[28]+65536*b[29];}
    else if(type==='VP8 ' && b[23]===157 && b[24]===1 && b[25]===42) {width=(b[26]+256*b[27])&16383;height=(b[28]+256*b[29])&16383;}
    else if(type==='VP8L' && b[20]===47) {width=1+b[21]+((b[22]&63)<<8);height=1+(b[22]>>6)+(b[23]<<2)+((b[24]&15)<<10);}
  } else {
    if(b.length>1024*1024)throw new Error('SVG exceeds the parsing limit');
    const text = new TextDecoder('utf-8',{fatal:true}).decode(b);
    if (!/^\s*(?:<\?xml[^>]*>\s*)?<svg[\s>]/u.test(text)) throw new Error('Unsupported image signature');
    const doc = new DOMParser().parseFromString(text, 'image/svg+xml');
    const svg=doc.documentElement;
    if(doc.querySelectorAll('*').length>5000)throw new Error('SVG exceeds the node limit');
    if (doc.querySelector('parsererror') || svg.localName !== 'svg' || doc.doctype || doc.querySelector('script,foreignObject,iframe,object,embed,style,animate,set,animateTransform,animateMotion')) throw new Error('SVG contains unsupported active content');
    for (const element of Array.from(doc.querySelectorAll('*'))) for (const attribute of Array.from(element.attributes)) {
      const value=attribute.value;
      if (attribute.localName==='style' || value.includes('\\') || /^on/iu.test(attribute.name) || /url\s*\(|@import/iu.test(value) || ((attribute.localName==='href' || attribute.localName==='src') && !value.startsWith('#'))) throw new Error('SVG external resources are unavailable');
    }
    const viewBox=(svg.getAttribute('viewBox') || '').trim().split(/[\s,]+/u).map(Number);
    width=Number.parseFloat(svg.getAttribute('width') || '') || viewBox[2];height=Number.parseFloat(svg.getAttribute('height') || '') || viewBox[3]; mime='image/svg+xml';
  }
  if (!Number.isFinite(width) || !Number.isFinite(height) || width<1 || height<1 || width>32768 || height>32768 || width*height>MAX_IMAGE_PIXELS) throw new Error('Image exceeds the decoded pixel limit or has unsupported dimensions');
  return { mime,width,height };
}
