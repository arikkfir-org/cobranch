// Draws the walk-throughs' diagrams as inline SVG. A node or participant with a link key opens that page.
window.URLS={"architecture":"architecture.html","admin-server":"admin-server.html","auth-server":"auth-server.html","controller":"controller.html","git-server":"git-server.html","graphql-server":"graphql-server.html","maintenance":"maintenance.html","migrate":"migrate.html","push-worker":"push-worker.html","rest-server":"rest-server.html","postgresql":"postgresql.html","back-office":"back-office.html","console":"console.html"};
const NS="http://www.w3.org/2000/svg";
function el(p,t,a,txt){const e=document.createElementNS(NS,t);for(const k in a)e.setAttribute(k,a[k]);if(txt!=null)e.textContent=txt;p.appendChild(e);return e}
function start(id,W,H){const s=document.getElementById(id);s.setAttribute("viewBox","0 0 "+W+" "+H);const m=el(el(s,"defs",{}),"marker",{id:id+"-a",viewBox:"0 0 10 10",refX:9,refY:5,markerWidth:9,markerHeight:9,orient:"auto"});el(m,"path",{d:"M0 0L10 5L0 10z",class:"ah"});return s}
function lines(s,cx,cy,label){const L=label.split("\n"),h=19;L.forEach((t,i)=>el(s,"text",{x:cx,y:cy-(L.length-1)*h/2+i*h+5,class:i?"t2":"t1"},t))}
function pt(b,tx,ty){const cx=b.x+b.w/2,cy=b.y+b.h/2,dx=tx-cx,dy=ty-cy;const k=Math.min(dx?b.w/2/Math.abs(dx):1e9,dy?b.h/2/Math.abs(dy):1e9);return [cx+dx*k,cy+dy*k]}
function graph(id,W,H,nodes,edges,groups){const s=start(id,W,H),N={};
  for(const g of groups||[]){el(s,"rect",{x:g[0],y:g[1],width:g[2],height:g[3],rx:12,class:"grp-r"});el(s,"text",{x:g[0]+12,y:g[1]+20,class:"gl"},g[4])}
  for(const [k,x,y,w,h] of nodes)N[k]={x,y,w,h};
  const labels=[];
  for(const [a,b,label,dash,ox=0,oy=0] of edges){const A=N[a],B=N[b],p=pt(A,B.x+B.w/2,B.y+B.h/2),q=pt(B,A.x+A.w/2,A.y+A.h/2);
    el(s,"line",{x1:p[0],y1:p[1],x2:q[0],y2:q[1],class:"e"+(dash?" dash":""),"marker-end":"url(#"+id+"-a)"});
    if(label)labels.push([(p[0]+q[0])/2+ox,(p[1]+q[1])/2+oy,label])}
  for(const [k,x,y,w,h,kind,label,link] of nodes){const u=link&&window.URLS&&URLS[link];const g=u?el(s,"a",{href:u}):s;el(g,"rect",{x,y,width:w,height:h,rx:8,class:"b k-"+kind});lines(g,x+w/2,y+h/2,label)}
  for(const [x,y,t] of labels)t.split("\n").forEach((l,i,a)=>el(s,"text",{x,y:y-(a.length-1)*8+i*16+4,class:"el"},l))}
// flow lays columns of nodes [key,kind,label,link] left to right and draws edges [from,to,label,dashed,dx,dy].
function flow(id,cols,edges,o={}){const W=1400,n=cols.length,gap=o.gap||90,w=Math.min(o.w||250,(W-40-(n-1)*gap)/n),step=n>1?(W-40-w)/(n-1):0,vg=o.vg||18;
  const hs=cols.map(c=>c.reduce((t,nd)=>t+30+19*nd[2].split("\n").length+vg,-vg)),H=Math.max(...hs)+50,nodes=[];
  cols.forEach((c,i)=>{let y=(H-hs[i])/2;for(const [k,kind,label,link] of c){const h=30+19*label.split("\n").length;nodes.push([k,20+i*step,y,w,h,kind,label,link]);y+=h+vg}});
  graph(id,W,H,nodes,edges)}
// seq draws a sequence: P are participants [label,kind,link]; M are [from,to,text,dashed], a self call when from===to, a note when to===null.
function seq(id,P,M){const X=i=>110+i*(1180/(P.length-1)),H=100+M.length*48,s=start(id,1400,H);
  P.forEach(([p,kind,link],i)=>{el(s,"line",{x1:X(i),y1:50,x2:X(i),y2:H-10,class:"life"});const u=link&&window.URLS&&URLS[link],g=u?el(s,"a",{href:u}):s;el(g,"rect",{x:X(i)-95,y:10,width:190,height:40,rx:8,class:"b k-"+kind});el(g,"text",{x:X(i),y:35,class:"t1"},p)});
  M.forEach(([a,b,t,dash],i)=>{const y=100+i*48;
   if(b===null){const w=t.length*7.4+30,x=Math.min(Math.max(X(a),w/2+10),1390-w/2);el(s,"rect",{x:x-w/2,y:y-22,width:w,height:32,rx:6,class:"note"});el(s,"text",{x,y:y-1,class:"t2",style:"fill:var(--fg)"},t);return}
   if(a===b){el(s,"path",{d:`M${X(a)} ${y-14}h36v18h-36`,class:"e","marker-end":"url(#"+id+"-a)"});const right=X(a)+48+t.length*7<1390;el(s,"text",{x:right?X(a)+48:X(a)-12,y,class:"el",style:"text-anchor:"+(right?"start":"end")},t);return}
   el(s,"line",{x1:X(a),y1:y,x2:X(b),y2:y,class:"e"+(dash?" dash":""),"marker-end":"url(#"+id+"-a)"});el(s,"text",{x:Math.min(Math.max((X(a)+X(b))/2,t.length*3.6+8),1392-t.length*3.6),y:y-8,class:"el"},t)})}
