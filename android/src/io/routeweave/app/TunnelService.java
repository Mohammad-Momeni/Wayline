package io.routeweave.app;

import android.app.*;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.net.*;
import android.os.*;
import android.system.*;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import java.text.SimpleDateFormat;
import org.json.*;

public final class TunnelService extends VpnService {
    static final String EVENT="io.routeweave.app.EVENT", STOP="io.routeweave.app.STOP";
    static volatile String pendingConfig;
    static volatile boolean running;
    static volatile String lastStatus="Disconnected", certificate="";
    static final ArrayList<String> history=new ArrayList<>();
    private final Handler main=new Handler(Looper.getMainLooper());
    private volatile Session active;

    @Override public synchronized int onStartCommand(Intent intent,int flags,int startId) {
        if(intent!=null && STOP.equals(intent.getAction())) {
            if(active!=null) active.cancel();
            return START_NOT_STICKY;
        }
        if(active!=null) return START_NOT_STICKY;
        String json=pendingConfig;
        pendingConfig=null;
        if(json==null) { stopSelf(); return START_NOT_STICKY; }
        try {
            Session session=new Session(new JSONObject(json));
            active=session;
            running=true;
            certificate="";
            foreground();
            new Thread(session,"Wayline-session").start();
        } catch(Exception error) {
            active=null; running=false;
            event("error","Cannot start session: "+error.getMessage());
            stopSelf();
        }
        return START_NOT_STICKY;
    }

    private void foreground() {
        NotificationManager manager=getSystemService(NotificationManager.class);
        manager.createNotificationChannel(new NotificationChannel("connection","VPN connection",NotificationManager.IMPORTANCE_LOW));
        PendingIntent open=PendingIntent.getActivity(this,1,new Intent(this,MainActivity.class),PendingIntent.FLAG_UPDATE_CURRENT|PendingIntent.FLAG_IMMUTABLE);
        PendingIntent stop=PendingIntent.getService(this,2,new Intent(this,TunnelService.class).setAction(STOP),PendingIntent.FLAG_UPDATE_CURRENT|PendingIntent.FLAG_IMMUTABLE);
        Notification notification=new Notification.Builder(this,"connection").setContentTitle("Wayline")
            .setContentText("VPN session active").setSmallIcon(getResources().getIdentifier("wayline_notification","drawable",getPackageName()))
            .setContentIntent(open).setOngoing(true)
            .addAction(new Notification.Action.Builder(null,"Disconnect",stop).build()).build();
        if(Build.VERSION.SDK_INT>=34) startForeground(1,notification,ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        else startForeground(1,notification);
    }

    private final class Session implements Runnable {
        final JSONObject config;
        final boolean reconnect;
        final AtomicBoolean cancelled=new AtomicBoolean();
        final CountDownLatch wake=new CountDownLatch(1);
        final ArrayList<String> secrets=new ArrayList<>();
        volatile Attempt attempt;
        volatile Network network;
        ConnectivityManager.NetworkCallback callback;
        PowerManager.WakeLock power;

        Session(JSONObject config) {
            this.config=config;
            reconnect=config.optBoolean("autoReconnect");
            secrets.add(config.optString("password"));
            for(String side:new String[]{"before","after"}) {
                JSONArray hops=config.optJSONArray(side);
                if(hops!=null) for(int i=0;i<hops.length();i++) secrets.add(hops.optJSONObject(i).optString("password"));
            }
        }
        void cancel() {
            cancelled.set(true);
            wake.countDown();
            Attempt current=attempt;
            if(current!=null) current.close();
        }
        void restart(Attempt expected,String reason) {
            if(cancelled.get() || attempt!=expected || expected.closed.get()) return;
            event("log",reason);
            expected.close();
        }
        void transportFailure(Attempt expected,String reason) {
            if(cancelled.get() || attempt!=expected || expected.closed.get()) return;
            if(!expected.transportFailed.compareAndSet(false,true)) return;
            event("log",reason+". Waiting for the engine's exit reason.");
            main.postDelayed(()->{
                if(!cancelled.get() && attempt==expected && !expected.closed.get()) restart(expected,"Packet bridge did not recover");
            },10000);
        }
        void watchNetwork() {
            ConnectivityManager connectivity=getSystemService(ConnectivityManager.class);
            network=connectivity.getActiveNetwork();
            callback=new ConnectivityManager.NetworkCallback() {
                @Override public void onAvailable(Network available) {
                    NetworkCapabilities caps=connectivity.getNetworkCapabilities(available);
                    if(caps!=null && caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) return;
                    Network previous=network;
                    network=available;
                    Attempt current=attempt;
                    if(reconnect && previous!=null && !previous.equals(available) && current!=null)
                        restart(current,"Underlying network changed");
                }
                @Override public void onLost(Network lost) {
                    if(lost.equals(network)) {
                        network=null;
                        Attempt current=attempt;
                        if(reconnect && current!=null) restart(current,"Underlying network disconnected");
                    }
                }
            };
            connectivity.registerDefaultNetworkCallback(callback);
        }
        @Override public void run() {
            long delay=2;
            try {
                power=getSystemService(PowerManager.class).newWakeLock(PowerManager.PARTIAL_WAKE_LOCK,"Wayline:VPN");
                power.setReferenceCounted(false);
                power.acquire();
                watchNetwork();
                while(!cancelled.get()) {
                    long started=SystemClock.elapsedRealtime();
                    Attempt current=new Attempt(this);
                    attempt=current;
                    try { current.runEngine(); }
                    catch(Exception error) {
                        if(error instanceof SecurityException) current.retryable=false;
                        if(!cancelled.get() && !current.closed.get())
                            event("error","Connection failed: "+error.getMessage());
                    } finally {
                        current.close();
                        current.awaitExit();
                        attempt=null;
                    }
                    if(cancelled.get() || !reconnect || !current.retryable) break;
                    if(SystemClock.elapsedRealtime()-started>60000) delay=2;
                    event("status","Reconnecting in "+delay+" seconds");
                    if(wake.await(delay,TimeUnit.SECONDS)) break;
                    delay=Math.min(delay*2,30);
                }
            } catch(Exception error) {
                if(!cancelled.get()) event("error","Session stopped: "+error.getMessage());
            } finally {
                cancel();
                if(callback!=null) try { getSystemService(ConnectivityManager.class).unregisterNetworkCallback(callback); } catch(Exception ignored) {}
                if(power!=null && power.isHeld()) power.release();
                main.post(()->finish(this));
            }
        }
    }

    private final class Attempt {
        final Session owner;
        final AtomicBoolean closed=new AtomicBoolean();
        final AtomicBoolean transportFailed=new AtomicBoolean();
        final CountDownLatch engineStopped=new CountDownLatch(1);
        volatile boolean retryable=true;
        volatile java.lang.Process child;
        volatile OutputStreamWriter input;
        volatile ParcelFileDescriptor tunnel;
        volatile DatagramSocket socket;
        int exitUid=-1;
        boolean exitSeen;
        long lastTime,lastIn,lastOut;
        final LinkedHashMap<String,Boolean> owners=new LinkedHashMap<String,Boolean>(256,0.75f,true) {
            @Override protected boolean removeEldestEntry(Map.Entry<String,Boolean> entry) { return size()>4096; }
        };

        Attempt(Session owner) { this.owner=owner; }
        void configure(JSONObject config) throws Exception {
            config.remove("androidLocalExit");
            JSONArray after=config.optJSONArray("after");
            if(after==null || after.length()==0) return;
            JSONObject first=after.getJSONObject(0);
            String host=first.optString("host");
            if(!"localhost".equalsIgnoreCase(host) && !host.matches("127\\.[0-9]+\\.[0-9]+\\.[0-9]+")) return;
            retryable=false;
            if(Build.VERSION.SDK_INT<29) throw new Exception("Local exit app routing requires Android 10+. Import its Xray configuration directly on this phone.");
            if(!"vpn".equals(config.optString("mode")) || !"socks5".equals(first.optString("type")))
                throw new Exception("Use a local SOCKS5 exit in VPN mode.");
            String packageName=first.optString("androidPackage");
            if(packageName.isEmpty()) throw new Exception("Choose the app providing the local After proxy.");
            exitUid=getPackageManager().getApplicationInfo(packageName,0).uid;
            if(exitUid==getApplicationInfo().uid) throw new Exception("Choose the external proxy app.");
            config.put("androidLocalExit",true);
            retryable=true;
        }
        void runEngine() throws Exception {
            JSONObject config=new JSONObject(owner.config.toString());
            config.put("autoReconnect",false);
            configure(config);
            Network physical=owner.network;
            if(physical!=null) {
                LinkProperties properties=getSystemService(ConnectivityManager.class).getLinkProperties(physical);
                if(properties!=null) {
                    JSONArray dns=new JSONArray();
                    for(InetAddress address:properties.getDnsServers()) dns.put(address.getHostAddress());
                    config.put("bootstrapDns",dns);
                }
            }
            File binary=new File(getApplicationInfo().nativeLibraryDir,"libwayline.so");
            if(!binary.isFile()) { retryable=false; throw new IOException("VPN engine missing for this CPU architecture."); }
            if(owner.cancelled.get()) return;
            event("status","Connecting");
            synchronized(this) {
                if(closed.get() || owner.cancelled.get()) return;
                child=new ProcessBuilder(binary.getAbsolutePath(),"--session").directory(getFilesDir()).start();
                input=new OutputStreamWriter(child.getOutputStream(),StandardCharsets.UTF_8);
                input.write(config.toString()+"\n");
                input.flush();
            }
            new Thread(()->{
                try(BufferedReader reader=new BufferedReader(new InputStreamReader(child.getErrorStream(),StandardCharsets.UTF_8))) {
                    String line;
                    while((line=reader.readLine())!=null && !closed.get()) event("log",line);
                } catch(IOException ignored) {}
            },"Wayline-diagnostics").start();
            try(BufferedReader reader=new BufferedReader(new InputStreamReader(child.getInputStream(),StandardCharsets.UTF_8))) {
                String line;
                while((line=reader.readLine())!=null && !closed.get()) {
                    JSONObject record=new JSONObject(line);
                    String type=record.optString("type");
                    if("tun".equals(type)) establish(record);
                    else if("certificate".equals(type)) {
                        retryable=false;
                        certificate=record.optString("sha256");
                        event("certificate","Gateway certificate rejected. Verify SHA-256 before trusting: "+certificate);
                    } else if("stats".equals(type)) statistics(record);
                    else {
                        if("error".equals(type)) retryable=record.optBoolean("retryable",false);
                        event(type,record.optString("message"));
                    }
                }
            }
            if(!closed.get()) event("log","Engine ended ("+child.waitFor()+")");
        }
        synchronized void establish(JSONObject record) throws Exception {
            if(closed.get() || owner.cancelled.get()) return;
            if(tunnel!=null || !"vpn".equals(owner.config.optString("mode"))) throw new IOException("Unexpected VPN interface request");
            int port=record.getInt("port");
            String token=record.getString("token");
            if(port<1 || port>65535 || token.isEmpty() || token.length()>128) throw new IOException("Invalid VPN packet channel");
            Builder builder=new Builder().setSession("Wayline").setMtu(record.optInt("mtu",1300))
                .addAddress(record.optString("address","10.77.0.2"),record.optInt("prefix",30))
                .addRoute("0.0.0.0",0).addDnsServer("198.18.0.1").setBlocking(false);
            applyAppFilter(builder);
            if(Build.VERSION.SDK_INT>=29) builder.setMetered(false);
            if(owner.network!=null) builder.setUnderlyingNetworks(new Network[]{owner.network});
            tunnel=builder.establish();
            if(tunnel==null) { retryable=false; throw new IOException("VPN permission was revoked. Reconnect and approve the request."); }
            socket=new DatagramSocket(new InetSocketAddress(InetAddress.getByName("127.0.0.1"),0));
            socket.setReceiveBufferSize(4<<20);
            socket.setSendBufferSize(4<<20);
            socket.connect(InetAddress.getByName("127.0.0.1"),port);
            byte[] authentication=token.getBytes(StandardCharsets.UTF_8);
            socket.send(new DatagramPacket(authentication,authentication.length));
            ParcelFileDescriptor descriptor=tunnel;
            DatagramSocket channel=socket;
            new Thread(()->pumpOut(descriptor,channel),"Wayline-tun-out").start();
            new Thread(()->pumpIn(descriptor,channel),"Wayline-tun-in").start();
            event("status","VPN interface ready");
        }
        void applyAppFilter(Builder builder)throws Exception {
            String mode=owner.config.optString("appFilterMode","all");JSONArray apps=owner.config.optJSONArray("appFilterApps");
            java.util.LinkedHashSet<String> selected=new java.util.LinkedHashSet<>();if(apps!=null)for(int i=0;i<apps.length();i++)if(!getPackageName().equals(apps.getString(i)))selected.add(apps.getString(i));
            JSONArray after=owner.config.optJSONArray("after");String provider="";if(exitUid>=0&&after!=null&&after.length()>0)provider=after.getJSONObject(0).optString("androidPackage");
            retryable=false;
            if("include".equals(mode)){
                if(selected.isEmpty())throw new IOException("Choose at least one app for Only selected apps.");
                if(!provider.isEmpty())selected.add(provider);
                for(String name:selected)builder.addAllowedApplication(name);
                event("log","VPN limited to "+selected.size()+" selected apps");
            }else{
                builder.addDisallowedApplication(getPackageName());
                if("exclude".equals(mode))for(String name:selected)if(!name.equals(provider))try{builder.addDisallowedApplication(name);}catch(android.content.pm.PackageManager.NameNotFoundException removed){event("log","An excluded app is no longer installed");}
                if("exclude".equals(mode))event("log",selected.size()+" apps excluded from VPN");
            }
            retryable=true;
        }
        void pumpOut(ParcelFileDescriptor descriptor,DatagramSocket channel) {
            try {
                byte[] bytes=new byte[65536];
                StructPollfd poll=new StructPollfd();
                poll.fd=descriptor.getFileDescriptor(); poll.events=(short)OsConstants.POLLIN;
                StructPollfd[] descriptors={poll};
                DatagramPacket packet=new DatagramPacket(bytes,0);
                while(!closed.get() && !owner.cancelled.get()) {
                    if(Os.poll(descriptors,500)==0) continue;
                    int count;
                    try { count=Os.read(poll.fd,bytes,1,bytes.length-1); }
                    catch(ErrnoException error) { if(error.errno==OsConstants.EAGAIN) continue; throw error; }
                    if(count<=0) throw new EOFException("VPN interface closed");
                    boolean raw=exitUid>=0 && rawExitPacket(bytes,1,count);
                    bytes[0]=0;
                    packet.setData(bytes,raw?0:1,count+(raw?1:0));
                    channel.send(packet);
                }
            } catch(Exception error) { owner.transportFailure(this,"VPN send channel stopped: "+error.getMessage()); }
        }
        void pumpIn(ParcelFileDescriptor descriptor,DatagramSocket channel) {
            try {
                byte[] bytes=new byte[65535];
                DatagramPacket packet=new DatagramPacket(bytes,bytes.length);
                int unreachable=0;
                while(!closed.get() && !owner.cancelled.get()) {
                    packet.setLength(bytes.length);
                    try {
                        channel.receive(packet);
                        unreachable=0;
                    } catch(PortUnreachableException temporary) {
                        if(child!=null && !child.isAlive()) return;
                        if(++unreachable>=3) throw temporary;
                        SystemClock.sleep(100);
                        continue;
                    }
                    for(;;) {
                        try {
                            int written=Os.write(descriptor.getFileDescriptor(),bytes,0,packet.getLength());
                            if(written!=packet.getLength()) throw new IOException("Incomplete VPN packet write");
                            break;
                        } catch(ErrnoException error) {
                            if(error.errno!=OsConstants.EAGAIN) throw error;
                            StructPollfd poll=new StructPollfd();
                            poll.fd=descriptor.getFileDescriptor(); poll.events=(short)OsConstants.POLLOUT;
                            Os.poll(new StructPollfd[]{poll},500);
                            if(closed.get() || owner.cancelled.get()) return;
                        }
                    }
                }
            } catch(Exception error) { owner.transportFailure(this,"VPN receive channel stopped: "+error.getMessage()); }
        }
        boolean rawExitPacket(byte[] packet,int start,int count) throws Exception {
            if(Build.VERSION.SDK_INT<29 || count<24 || (packet[start]>>4 & 15)!=4) return false;
            if((packet[start+6]&31)!=0 || packet[start+7]!=0) return false;
            int offset=start+(packet[start]&15)*4;
            int protocol=packet[start+9]&255;
            if(offset<start+20 || start+count<offset+4 || (protocol!=6 && protocol!=17)) return false;
            InetAddress source=InetAddress.getByAddress(Arrays.copyOfRange(packet,start+12,start+16));
            InetAddress destination=InetAddress.getByAddress(Arrays.copyOfRange(packet,start+16,start+20));
            int sourcePort=(packet[offset]&255)*256+(packet[offset+1]&255);
            int destinationPort=(packet[offset+2]&255)*256+(packet[offset+3]&255);
            String key=protocol+"/"+source+":"+sourcePort+"/"+destination+":"+destinationPort;
            if(protocol==6 && start+count>offset+13 && (packet[offset+13]&0x12)==2) owners.remove(key);
            Boolean cached=owners.get(key);
            if(cached!=null) return cached;
            int uid=getSystemService(ConnectivityManager.class).getConnectionOwnerUid(protocol,
                new InetSocketAddress(source,sourcePort),new InetSocketAddress(destination,destinationPort));
            boolean raw=uid==exitUid;
            if(uid>=0 && protocol==6) owners.put(key,raw);
            if(raw && !exitSeen) { exitSeen=true; event("log","Local proxy app routed through the VPN"); }
            return raw;
        }
        void statistics(JSONObject record) {
            long now=SystemClock.elapsedRealtime(),incoming=record.optLong("bytesIn"),outgoing=record.optLong("bytesOut");
            double seconds=lastTime==0?1:Math.max(.001,(now-lastTime)/1000.0);
            double down=Math.max(0,incoming-lastIn)*8/seconds/1000000.0;
            double up=Math.max(0,outgoing-lastOut)*8/seconds/1000000.0;
            lastTime=now; lastIn=incoming; lastOut=outgoing;
            event("stats",String.format(Locale.US,"↓ %.2f Mbps  ↑ %.2f Mbps\nReceived %.1f MB · Sent %.1f MB",down,up,incoming/1048576.0,outgoing/1048576.0));
        }
        synchronized void close() {
            if(!closed.compareAndSet(false,true)) return;
            // Start independently of the stdout reader. EOF alone cannot stop
            // a wedged child, and runEngine may still be blocked in readLine.
            java.lang.Process process=child;
            new Thread(()->{
                try {
                    if(process!=null && !process.waitFor(8,TimeUnit.SECONDS)) {
                        event("log","Engine did not stop after disconnect; terminating it");
                        process.destroy();
                        if(!process.waitFor(2,TimeUnit.SECONDS)) process.destroyForcibly();
                    }
                } catch(InterruptedException error) {
                    Thread.currentThread().interrupt();
                    if(process!=null) process.destroyForcibly();
                } finally { engineStopped.countDown(); }
            },"Wayline-engine-stop").start();
            if(socket!=null) socket.close();
            if(tunnel!=null) try { tunnel.close(); } catch(IOException ignored) {}
            if(input!=null) try { input.close(); } catch(IOException ignored) {}
        }
        void awaitExit() {
            try { engineStopped.await(12,TimeUnit.SECONDS); }
            catch(InterruptedException error) { Thread.currentThread().interrupt(); }
        }
    }
    private synchronized void finish(Session session) {
        if(active!=session) return;
        active=null; running=false;
        stopForeground(STOP_FOREGROUND_REMOVE);
        stopSelf();
        event("status","Disconnected");
    }
    private void event(String type,String text) {
        text=text==null?"":text;
        Session session=active;
        if(session!=null) for(String secret:session.secrets) if(!secret.isEmpty()) text=text.replace(secret,"[redacted]");
        if(!"stats".equals(type)) synchronized(history) { history.add(new SimpleDateFormat("HH:mm:ss",Locale.US).format(new Date())+"  "+text); while(history.size()>160) history.remove(0); }
        if("status".equals(type)||"error".equals(type)) lastStatus=text;
        sendBroadcast(new Intent(EVENT).setPackage(getPackageName()).putExtra("type",type).putExtra("message",text));
    }
    @Override public void onRevoke() { Session session=active; if(session!=null) session.cancel(); super.onRevoke(); }
    @Override public void onDestroy() { Session session=active; if(session!=null) session.cancel(); super.onDestroy(); }
}
