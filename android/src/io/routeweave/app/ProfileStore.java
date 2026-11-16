package io.routeweave.app;

import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.Base64;
import java.security.KeyStore;
import java.util.UUID;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;
import org.json.JSONArray;
import org.json.JSONObject;

/** Profile metadata and Android Keystore protected secrets stay separate. */
final class ProfileStore {
    private static final String ALIAS = "io.routeweave.app.credentials.v1";
    private final SharedPreferences prefs;
    ProfileStore(Context context) { prefs = context.getSharedPreferences("routeweave", Context.MODE_PRIVATE); }
    static String id() { return UUID.randomUUID().toString(); }
    static JSONObject defaults() throws Exception {
        return new JSONObject().put("id", id()).put("name", "New connection").put("gateway", "").put("port", 443)
          .put("username", "").put("realm", "").put("certificateSha256", "").put("mode", "proxy")
          .put("listenPort", 1080).put("rememberPassword", false).put("before", new JSONArray()).put("after", new JSONArray()).put("dns", new JSONArray());
    }
    JSONArray load() throws Exception {
        String saved = prefs.getString("profiles", null);
        if (saved == null) return new JSONArray().put(defaults());
        JSONArray profiles = new JSONArray(saved);
        if (profiles.length() == 0) profiles.put(defaults());
        return profiles;
    }
    void save(JSONArray profiles) throws Exception {
        JSONArray safe=new JSONArray(profiles.toString());
        for(int p=0;p<safe.length();p++) {
            JSONObject profile=safe.getJSONObject(p); profile.remove("password");
            for(String side:new String[]{"before","after"}) { JSONArray hops=profile.optJSONArray(side); if(hops!=null) for(int h=0;h<hops.length();h++) hops.getJSONObject(h).remove("password"); }
        }
        if (!prefs.edit().putString("profiles", safe.toString()).commit()) throw new Exception("Could not save profiles.");
    }
    private synchronized SecretKey key() throws Exception {
        KeyStore store = KeyStore.getInstance("AndroidKeyStore"); store.load(null);
        if (store.containsAlias(ALIAS)) return ((KeyStore.SecretKeyEntry)store.getEntry(ALIAS, null)).getSecretKey();
        KeyGenerator generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
        generator.init(new KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
          .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build());
        return generator.generateKey();
    }
    void putSecret(String id, String secret) throws Exception {
        if (secret.isEmpty()) { prefs.edit().remove("secret." + id).apply(); return; }
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.ENCRYPT_MODE, key());
        String encrypted = Base64.encodeToString(cipher.getIV(), Base64.NO_WRAP) + ":" + Base64.encodeToString(cipher.doFinal(secret.getBytes("UTF-8")), Base64.NO_WRAP);
        if (!prefs.edit().putString("secret." + id, encrypted).commit()) throw new Exception("Could not save encrypted credential.");
    }
    String getSecret(String id) throws Exception {
        String saved = prefs.getString("secret." + id, ""); if (saved.isEmpty()) return "";
        String[] pieces = saved.split(":", 2); if (pieces.length != 2) throw new Exception("Saved credential format is invalid.");
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding"); cipher.init(Cipher.DECRYPT_MODE, key(), new GCMParameterSpec(128, Base64.decode(pieces[0], Base64.NO_WRAP)));
        return new String(cipher.doFinal(Base64.decode(pieces[1], Base64.NO_WRAP)), "UTF-8");
    }
    void remove(JSONObject profile) throws Exception {
        putSecret(profile.getString("id"), "");
        for (String side : new String[]{"before", "after"}) { JSONArray hops = profile.optJSONArray(side); if (hops != null) for (int i=0;i<hops.length();i++) putSecret(hops.getJSONObject(i).optString("id"), ""); }
    }
    static JSONObject exported(JSONObject profile, boolean redactUser) throws Exception {
        JSONObject copy = new JSONObject(profile.toString()); copy.remove("password"); copy.put("rememberPassword", false);copy.put("appFilterMode","all");copy.remove("appFilterApps");
        for (String key : new String[]{"xrays", "xrayConfigId", "xrayOutboundTag", "xrayConfig", "xrayExecutable", "xrayConfigFile", "xrayExit"}) copy.remove(key);
        if (redactUser) copy.put("username", "");
        for (String side : new String[]{"before", "after"}) {
            JSONArray hops = copy.optJSONArray(side); if(hops!=null)for(int i=hops.length()-1;i>=0;i--)if("xray".equals(hops.getJSONObject(i).optString("type")))hops.remove(i); if (hops == null) { copy.put(side, new JSONArray()); continue; }
            for (int i=0;i<hops.length();i++) { JSONObject h=hops.getJSONObject(i); h.remove("password"); h.put("rememberPassword",false); if(redactUser) h.put("username", ""); }
        }
        return copy;
    }
    static JSONObject imported(JSONObject source) throws Exception {
        JSONObject copy=exported(source,false); copy.put("id",id());
        for(String side:new String[]{"before","after"}) { JSONArray a=copy.getJSONArray(side); for(int i=0;i<a.length();i++) a.getJSONObject(i).put("id",id()); }
        return copy;
    }
}
