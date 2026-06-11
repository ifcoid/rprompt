import math
from PIL import Image, ImageDraw

S = 2048  # supersample canvas
def lerp(a, b, t): return tuple(int(a[i] + (b[i]-a[i])*t) for i in range(3))

# diagonal gradient background
c0=(0x6D,0x5B,0xFF); c1=(0x4D,0x6B,0xFF); c2=(0x2B,0xB7,0xFF)
bg = Image.new("RGBA",(S,S),(0,0,0,0))
px = bg.load()
for y in range(S):
    for x in range(0, S, 1):
        t=(x+y)/(2*S)
        col = lerp(c0,c1,t/0.55) if t<0.55 else lerp(c1,c2,(t-0.55)/0.45)
        px[x,y]=(col[0],col[1],col[2],255)

# rounded-rect mask (Apple-tile)
mask = Image.new("L",(S,S),0)
md = ImageDraw.Draw(mask)
m = int(16/512*S); r = int(112/512*S)
md.rounded_rectangle([m,m,S-m,S-m], radius=r, fill=255)
tile = Image.new("RGBA",(S,S),(0,0,0,0))
tile.paste(bg,(0,0),mask)

d = ImageDraw.Draw(tile)
def sc(v): return v/512*S
W=int(sc(26))
def line(pts,width=W,fill=(255,255,255,255)):
    d.line([(sc(x),sc(y)) for x,y in pts], fill=fill, width=width, joint="curve")
    rr=width//2
    for x,y in (pts[0],pts[-1]):
        d.ellipse([sc(x)-rr,sc(y)-rr,sc(x)+rr,sc(y)+rr], fill=fill)

def bez(p0,p1,p2,p3,n=60):
    out=[]
    for i in range(n+1):
        t=i/n; u=1-t
        x=u**3*p0[0]+3*u*u*t*p1[0]+3*u*t*t*p2[0]+t**3*p3[0]
        y=u**3*p0[1]+3*u*u*t*p1[1]+3*u*t*t*p2[1]+t**3*p3[1]
        out.append((x,y))
    return out

WH=(255,255,255,255); WHd=(255,255,255,235)
# --- LEFT: prompt cue ">_" (terminal) ---
line([(150,206),(206,256),(150,306)], fill=WH)      # chevron ">"
d.rounded_rectangle([sc(150),sc(330),sc(224),sc(352)], radius=sc(11), fill=WH)  # underscore "_"

# --- HUB node (the proxy core) ---
hub=(276,256)
rr=sc(30); d.ellipse([sc(hub[0])-rr,sc(hub[1])-rr,sc(hub[0])+rr,sc(hub[1])+rr], fill=WH)

# --- RIGHT: fan-out routes to engine nodes ---
line(bez((276,256),(330,256),(348,154),(392,154)), fill=WHd)
line([(276,256),(392,256)], fill=WHd)
line(bez((276,256),(330,256),(348,358),(392,358)), fill=WHd)
for cy in (154,256,358):
    rr=sc(26); cx=sc(392); cyy=sc(cy)
    d.ellipse([cx-rr,cyy-rr,cx+rr,cyy+rr], fill=WH)

# export
sizes=[512,256,128,64,48,32,16]
imgs={}
for s in sizes:
    im=tile.resize((s,s), Image.LANCZOS)
    imgs[s]=im
    im.save(f"icon-{s}.png")
tile.resize((1024,1024),Image.LANCZOS).save("icon.png")
imgs[512].save("icon.ico", sizes=[(16,16),(32,32),(48,48),(64,64),(128,128),(256,256)])
print("done")
