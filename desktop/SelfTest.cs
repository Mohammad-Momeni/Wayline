using System.IO;
using System.Text.Json;
namespace Wayline;

public static class SelfTest
{
    public static void Run()
    {
        var report = Path.Combine(AppContext.BaseDirectory, "desktop-self-test.txt");
        var results = new List<string>();
        try
        {
            var blank = new Profile();
            if (blank.Gateway != "" || blank.Username != "" || blank.CertificateSha256 != "" || blank.Before.Count != 0 || blank.After.Count != 0)
                throw new Exception("New profiles contain connection data.");
            if (!File.Exists(Store.FilePath) && Store.Load().Count != 0)
                throw new Exception("A clean install created saved profile data.");
            results.Add("PASS: new profiles are blank and clean installations have no saved profile data.");
            var p = new Profile { Name = "roundtrip", Before = [new Hop { Password = "must-never-appear", Username = "user" }], After = [new Hop { Host = "exit.test", Port = 443, Password = "also-secret" }] };
            string json = JsonSerializer.Serialize(p, Store.Options);
            if (json.Contains("must-never-appear") || json.Contains("also-secret") || json.Contains("\"Password\"")) throw new Exception("Profile serialization contains passwords.");
            var restored = JsonSerializer.Deserialize<Profile>(json, Store.Options)!;
            if (restored.Before.Count != 1 || restored.After[0].Host != "exit.test" || restored.Before[0].Password != "") throw new Exception("Profile roundtrip failed.");
            if (!restored.Before[0].Enabled) throw new Exception("Legacy hops must default to enabled.");
            restored.Before[0].Enabled = false;
            var disabled = JsonSerializer.Deserialize<Profile>(JsonSerializer.Serialize(restored, Store.Options), Store.Options)!;
            if (disabled.Before[0].Enabled || !disabled.After[0].Enabled) throw new Exception("Hop enabled state was lost.");
            results.Add("PASS: profile roundtrip preserves both chain directions; passwords are excluded.");
            var saved = new SavedXray { Name = "Test exit", Exit = new XrayExit("xray-test", "private-test.json", "exit") };
            restored.Xrays.Add(saved);
            restored.After.Add(new Hop { Type = "xray", XrayId = saved.Id, Enabled = false });
            restored.Before.Add(new Hop { Type = "xray", XrayId = saved.Id });
            restored.AutoReconnect = true;restored.TrustedCertificates.Add(new string('a',64));restored.AppFilterMode="include";restored.AppFilterApps.Add(@"C:\Apps\browser.exe");var stableId=restored.Id;restored.Name="Renamed profile";
            var library = JsonSerializer.Deserialize<Profile>(JsonSerializer.Serialize(restored, Store.Options), Store.Options)!;
            if(library.Id!=stableId||library.Name!="Renamed profile"||library.TrustedCertificates.Count!=1||library.AppFilterMode!="include"||library.AppFilterApps.Count!=1)throw new Exception("Rename, trust list or app filter did not persist.");
            if (library.Xrays.Single().Name != "Test exit" || !library.AutoReconnect || library.After.Last().Enabled)
                throw new Exception("Xray library or reconnect state was lost.");
            var wire = JsonSerializer.Serialize(library.WireHop(library.Before.Last()), Store.Options);
            if (!wire.Contains("private-test.json") || !wire.Contains("outboundTag")) throw new Exception("Xray chain resolution failed.");
            if (!WindowsStartup.Command.StartsWith("\"") || !WindowsStartup.Command.EndsWith("\" --startup")) throw new Exception("Startup executable is not quoted.");
            results.Add("PASS: named Xray references, independent hop states, reconnect persistence and quoted startup command.");
            string id = "self-test-" + Guid.NewGuid().ToString("N");
            bool written = false;
            try
            {
                Vault.Set(id, "temporary-credential-✓");
                written = true;
                if (Vault.Get(id) != "temporary-credential-✓") throw new Exception("Credential Manager roundtrip failed.");
                results.Add("PASS: Windows Credential Manager Unicode roundtrip.");
            }
            catch (System.ComponentModel.Win32Exception e) when (e.NativeErrorCode == 1312) { results.Add("SKIP: Credential Manager roundtrip unavailable in sandbox (Windows error 1312: no interactive logon session)."); }
            finally { if (written) Vault.Remove(id); }
            if (written) { if (Vault.Get(id) != "") throw new Exception("Credential Manager deletion failed."); results.Add("PASS: credential deletion; no test secret retained."); }
            results.Add("DESKTOP LOGIC CHECKS FINISHED");
        }
        catch (Exception e) { results.Add("FAIL: " + e.Message); Environment.ExitCode = 1; }
        File.WriteAllLines(report, results);
    }
}
