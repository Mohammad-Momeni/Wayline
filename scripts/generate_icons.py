"""Generate Wayline's vector and raster application icons. Requires Pillow."""
from pathlib import Path
from PIL import Image, ImageDraw
ROOT=Path(__file__).resolve().parents[1]
points=[(226,304),(366,714),(512,392),(658,714),(798,304)]
svg='''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><defs><linearGradient id="bg" x2=".8" y2="1"><stop stop-color="#203550"/><stop offset="1" stop-color="#0C1524"/></linearGradient></defs><rect width="1024" height="1024" rx="230" fill="url(#bg)"/><path d="M226 304L366 714L512 392L658 714L798 304" fill="none" stroke="#70E4C3" stroke-width="78" stroke-linecap="round" stroke-linejoin="round"/><circle cx="226" cy="304" r="48" fill="#AAB8FF"/><circle cx="798" cy="304" r="48" fill="#AAB8FF"/></svg>'''
(ROOT/'assets/wayline.svg').write_text(svg,encoding='utf-8')
scale=2;image=Image.new('RGBA',(1024*scale,1024*scale));mask=Image.new('L',image.size);ImageDraw.Draw(mask).rounded_rectangle((0,0,2047,2047),radius=460,fill=255)
draw=ImageDraw.Draw(image)
for y in range(2048):
 t=y/2047;draw.line((0,y,2047,y),fill=tuple(int(a+(b-a)*t) for a,b in zip((32,53,80),(12,21,36)))+(255,))
image.putalpha(mask);draw=ImageDraw.Draw(image);pts=[(x*scale,y*scale) for x,y in points];draw.line(pts,fill='#70E4C3',width=78*scale,joint='curve')
for x,y in pts:draw.ellipse((x-39*scale,y-39*scale,x+39*scale,y+39*scale),fill='#70E4C3')
for x,y in [pts[0],pts[-1]]:draw.ellipse((x-48*scale,y-48*scale,x+48*scale,y+48*scale),fill='#AAB8FF')
image=image.resize((1024,1024),Image.Resampling.LANCZOS)
for destination in ['assets/wayline.png','desktop/Assets/wayline.png']:image.save(ROOT/destination)
image.save(ROOT/'desktop/Assets/wayline.ico',sizes=[(16,16),(24,24),(32,32),(48,48),(64,64),(128,128),(256,256)])
for p in (ROOT/'android/res').glob('drawable*/wayline.png'):image.resize((192,192),Image.Resampling.LANCZOS).save(p)
xml='''<vector xmlns:android="http://schemas.android.com/apk/res/android" android:width="48dp" android:height="48dp" android:viewportWidth="1024" android:viewportHeight="1024"><path android:fillColor="#15253A" android:pathData="M230,0 L794,0 Q1024,0 1024,230 L1024,794 Q1024,1024 794,1024 L230,1024 Q0,1024 0,794 L0,230 Q0,0 230,0"/><path android:fillColor="@android:color/transparent" android:strokeColor="#70E4C3" android:strokeWidth="78" android:strokeLineCap="round" android:strokeLineJoin="round" android:pathData="M226,304 L366,714 L512,392 L658,714 L798,304"/><path android:fillColor="#AAB8FF" android:pathData="M178,304 a48,48 0,1 0,96 0 a48,48 0,1 0,-96 0 M750,304 a48,48 0,1 0,96 0 a48,48 0,1 0,-96 0"/></vector>'''
(ROOT/'android/res/drawable/wayline.xml').write_text(xml)
(ROOT/'android/res/drawable/wayline_notification.xml').write_text('''<vector xmlns:android="http://schemas.android.com/apk/res/android" android:width="24dp" android:height="24dp" android:viewportWidth="1024" android:viewportHeight="1024"><path android:fillColor="@android:color/transparent" android:strokeColor="#FFFFFFFF" android:strokeWidth="100" android:strokeLineCap="round" android:strokeLineJoin="round" android:pathData="M150,250 L330,780 L512,350 L694,780 L874,250"/></vector>''')
print('Generated Wayline icon set.')
