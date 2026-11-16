using System.Runtime.InteropServices;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Wayline;

public sealed class Hop
{
    public string XrayId { get; set; } = "";
    [JsonIgnore] public string XrayName { get; set; } = "Missing configuration";
    public bool Enabled { get; set; } = true;
    public string Id { get; set; } = Guid.NewGuid().ToString("N");
    public string Type { get; set; } = "socks5";
    public string Host { get; set; } = "127.0.0.1";
    public int Port { get; set; } = 1080;
    public string Username { get; set; } = "";
    public bool RememberPassword { get; set; }
    [JsonIgnore] public string Password { get; set; } = "";
    public override string ToString() => Type == "xray" ? $"Xray · {XrayName}" : $"{Type.ToUpperInvariant()}  {Host}:{Port}";
    public object Wire() => new { type = Type, host = Host, port = Port, username = Username, password = Password };
}
public sealed class Profile
{
    public string AppFilterMode { get; set; } = "all";
    public List<string> AppFilterApps { get; set; } = [];
    public List<string> TrustedCertificates { get; set; } = [];
    public bool AutoReconnect { get; set; }
    public List<SavedXray> Xrays { get; set; } = [];
    public string Id { get; set; } = Guid.NewGuid().ToString("N");
    public string Name { get; set; } = "New connection";
    public string Gateway { get; set; } = "";
    public int Port { get; set; } = 443;
    public string Username { get; set; } = "";
    public string Realm { get; set; } = "";
    public string CertificateSha256 { get; set; } = "";
    public string Mode { get; set; } = "proxy";
    public int ListenPort { get; set; } = 1080;
    public bool RememberPassword { get; set; }
    public List<string> Dns { get; set; } = [];
    public List<Hop> Before { get; set; } = [];
    public List<Hop> After { get; set; } = [];
    public XrayExit? XrayExit { get; set; }
    public override string ToString() => Name;
    public object WireHop(Hop hop)
    {
        if (hop.Type != "xray") return hop.Wire();
        var saved = Xrays.SingleOrDefault(x => x.Id == hop.XrayId) ?? throw new InvalidOperationException("Select an existing saved Xray configuration.");
        return new { type = "xray", xray = saved.Exit };
    }
}
public sealed class SavedXray
{
    public bool OwnedFile { get; set; }
    public string Id { get; set; } = Guid.NewGuid().ToString("N");
    public string Name { get; set; } = "Xray";
    public XrayExit Exit { get; set; } = new("", "", "");
    public override string ToString() => Name;
}
public static class Store
{
    public static readonly JsonSerializerOptions Options = new() { WriteIndented = true, PropertyNameCaseInsensitive = true, PropertyNamingPolicy = JsonNamingPolicy.CamelCase };
    public static string DirectoryPath => Environment.GetEnvironmentVariable("WAYLINE_DATA_DIR") is { Length: > 0 } path ? Path.GetFullPath(path) : Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Routeweave");
    public static string FilePath => Path.Combine(DirectoryPath, "profiles.json");
    public static List<Profile> Load()
    {
        if (File.Exists(FilePath))
        {
            var profiles = JsonSerializer.Deserialize<List<Profile>>(File.ReadAllText(FilePath), Options) ?? [];
            foreach (var profile in profiles)
                if (profile.XrayExit is { } legacy)
                {
                    var saved = new SavedXray { Name = "Imported Xray", Exit = legacy };
                    profile.Xrays.Add(saved);
                    profile.After.Insert(0, new Hop { Type = "xray", XrayId = saved.Id });
                    profile.XrayExit = null;
                }
            return profiles;
        }
        return [];
    }
    public static void Save(List<Profile> profiles)
    {
        Directory.CreateDirectory(DirectoryPath);
        var temp = FilePath + ".tmp";
        File.WriteAllText(temp, JsonSerializer.Serialize(profiles, Options));
        File.Move(temp, FilePath, true);
    }
}

public static class Vault
{
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct Credential
    {
        public uint Flags, Type;
        public string TargetName;
        public string? Comment;
        public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
        public uint CredentialBlobSize;
        public IntPtr CredentialBlob;
        public uint Persist, AttributeCount;
        public IntPtr Attributes;
        public string? TargetAlias, UserName;
    }
    [DllImport("advapi32.dll", EntryPoint = "CredWriteW", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern bool Write(ref Credential credential, uint flags);
    [DllImport("advapi32.dll", EntryPoint = "CredReadW", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern bool Read(string target, uint type, uint flags, out IntPtr credential);
    [DllImport("advapi32.dll", EntryPoint = "CredDeleteW", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern bool Delete(string target, uint type, uint flags);
    [DllImport("advapi32.dll")] static extern void CredFree(IntPtr ptr);
    static string Key(string id) => "Routeweave/" + id;
    public static void Set(string id, string value)
    {
        if (value.Length == 0) { Remove(id); return; }
        byte[] bytes = Encoding.Unicode.GetBytes(value);
        if (bytes.Length > 2560) throw new InvalidOperationException("Credential exceeds Windows Credential Manager's limit.");
        IntPtr ptr = Marshal.AllocHGlobal(bytes.Length);
        try
        {
            Marshal.Copy(bytes, 0, ptr, bytes.Length);
            var c = new Credential { Type = 1, TargetName = Key(id), UserName = "Wayline", CredentialBlobSize = (uint)bytes.Length, CredentialBlob = ptr, Persist = 2 };
            if (!Write(ref c, 0)) throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
        }
        finally { for (int i = 0; i < bytes.Length; i++) Marshal.WriteByte(ptr, i, 0); Marshal.FreeHGlobal(ptr); Array.Clear(bytes); }
    }
    public static string Get(string id)
    {
        if (!Read(Key(id), 1, 0, out var ptr))
        {
            int error = Marshal.GetLastWin32Error();
            if (error == 1168) return "";
            throw new System.ComponentModel.Win32Exception(error);
        }
        try { var c = Marshal.PtrToStructure<Credential>(ptr); return Marshal.PtrToStringUni(c.CredentialBlob, (int)c.CredentialBlobSize / 2) ?? ""; }
        finally { CredFree(ptr); }
    }
    public static void Remove(string id)
    {
        if (!Delete(Key(id), 1, 0) && Marshal.GetLastWin32Error() != 1168) throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
    }
}
