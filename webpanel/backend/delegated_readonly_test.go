package main

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
)

func TestDelegatedQuotaReadOnlyPolicy(t *testing.T) {
    now := time.Now().UTC()
    cases := []struct {
        name string
        admin DelegatedAdmin
        login, mutation, readonly bool
    }{
        {"active", DelegatedAdmin{Active:true, ExpiresAt:now.Add(time.Hour), TrafficLimitBytes:100, TrafficUsedBytes:10}, true, true, false},
        {"expired", DelegatedAdmin{Active:true, ExpiresAt:now.Add(-time.Second), TrafficLimitBytes:100, TrafficUsedBytes:10, Suspended:true, SuspendReason:"expired"}, true, false, true},
        {"traffic", DelegatedAdmin{Active:true, ExpiresAt:now.Add(time.Hour), TrafficLimitBytes:100, TrafficUsedBytes:100, Suspended:true, SuspendReason:"traffic_exhausted"}, true, false, true},
        {"both", DelegatedAdmin{Active:true, ExpiresAt:now.Add(-time.Second), TrafficLimitBytes:100, TrafficUsedBytes:100, Suspended:true, SuspendReason:"expired"}, true, false, true},
        {"manual_disable", DelegatedAdmin{Active:false, ExpiresAt:now.Add(time.Hour), TrafficLimitBytes:100, TrafficUsedBytes:10, Suspended:true, SuspendReason:"disabled"}, false, false, false},
    }
    for _, tt := range cases {
        t.Run(tt.name,func(t *testing.T) {
            if got:=delegatedLoginPermitted(tt.admin,now);got!=tt.login {t.Fatalf("login=%v want %v",got,tt.login)}
            if got:=delegatedMutationAllowed(tt.admin,now);got!=tt.mutation {t.Fatalf("mutation=%v want %v",got,tt.mutation)}
            if got:=delegatedQuotaReadOnly(tt.admin,now);got!=tt.readonly {t.Fatalf("readonly=%v want %v",got,tt.readonly)}
        })
    }
}

func TestDelegatedExpiredLoginReadOnlyAndRenewal(t *testing.T) {
    oldData, oldAdmins, oldRoot, oldJWT := DataBaseDir, DelegatedAdminsDBPath, AdminDBPath, JWTSecret
    dir := t.TempDir()
    DataBaseDir=dir
    DelegatedAdminsDBPath=filepath.Join(dir,"webpanel","delegated_admins.json")
    AdminDBPath=filepath.Join(dir,"webpanel","admin.json")
    JWTSecret="delegated-readonly-test-secret"
    t.Cleanup(func(){DataBaseDir,DelegatedAdminsDBPath,AdminDBPath,JWTSecret=oldData,oldAdmins,oldRoot,oldJWT})
    if err:=os.MkdirAll(filepath.Join(dir,"database","clients"),0755);err!=nil {t.Fatal(err)}
    if err:=os.MkdirAll(filepath.Join(dir,"webpanel"),0755);err!=nil {t.Fatal(err)}
    admin:=DelegatedAdmin{
        Username:"resellertest", PasswordHash:hashPassword("strong-pass-123"),
        Active:true, TrafficLimitBytes:100,TrafficUsedBytes:30,
        ExpiresAt:time.Now().UTC().Add(-time.Hour),
        Suspended:true,SuspendReason:"expired",
    }
    if err:=saveDelegatedAdminsLocked([]DelegatedAdmin{admin});err!=nil{t.Fatal(err)}

    login:=httptest.NewRecorder()
    delegatedLoginHandler(login,httptest.NewRequest(http.MethodPost,"/api/login",
        strings.NewReader(`{"username":"resellertest","password":"strong-pass-123"}`)))
    if login.Code!=http.StatusOK {t.Fatalf("expired login status=%d body=%s",login.Code,login.Body.String())}
    var payload struct {Data struct {Token string `json:"token"`} `json:"data"`}
    if err:=json.Unmarshal(login.Body.Bytes(),&payload);err!=nil{t.Fatal(err)}
    if payload.Data.Token=="" {t.Fatal("login missing token")}

    handler:=delegatedAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
        if currentDelegatedPrincipal(r).Role!=delegatedRoleAdmin {t.Error("incorrect delegated role")}
        w.WriteHeader(http.StatusNoContent)
    }))
    call:=func(method,path string)int{
        t.Helper()
        req:=httptest.NewRequest(method,path,nil)
        req.Header.Set("Authorization","Bearer "+payload.Data.Token)
        rec:=httptest.NewRecorder()
        handler.ServeHTTP(rec,req)
        return rec.Code
    }
    if got:=call(http.MethodGet,"/api/me");got!=http.StatusNoContent{t.Fatalf("expired /me=%d",got)}
    if got:=call(http.MethodGet,"/api/clients");got!=http.StatusNoContent{t.Fatalf("expired /clients GET=%d",got)}
    for _,tt:=range []struct{method,path string}{
        {http.MethodPost,"/api/clients"},
        {http.MethodPut,"/api/clients/test"},
        {http.MethodDelete,"/api/clients/test"},
        {http.MethodPost,"/api/clients/test/deactivate"},
    }{
        if got:=call(tt.method,tt.path);got!=http.StatusForbidden {
            t.Fatalf("expired %s %s status=%d",tt.method,tt.path,got)
        }
    }

    delegatedAdminsMu.Lock()
    list,err:=loadDelegatedAdminsLocked()
    if err==nil {
        list[0].ExpiresAt=time.Now().UTC().Add(time.Hour)
        err=saveDelegatedAdminsLocked(list)
    }
    delegatedAdminsMu.Unlock()
    if err!=nil {t.Fatal(err)}
    if got:=call(http.MethodPost,"/api/clients");got!=http.StatusNoContent{
        t.Fatalf("renewed POST status=%d",got)
    }
    // The same token stays valid after the quota reaches its limit.
    delegatedAdminsMu.Lock()
    list,err=loadDelegatedAdminsLocked()
    if err==nil{
        list[0].TrafficUsedBytes=100
        err=saveDelegatedAdminsLocked(list)
    }
    delegatedAdminsMu.Unlock()
    if err!=nil {t.Fatal(err)}
    if got:=call(http.MethodGet,"/api/me");got!=http.StatusNoContent{t.Fatalf("traffic exhausted GET=%d",got)}
    if got:=call(http.MethodPost,"/api/clients");got!=http.StatusForbidden{t.Fatalf("traffic exhausted POST=%d",got)}

    delegatedAdminsMu.Lock()
    list,err=loadDelegatedAdminsLocked()
    if err==nil{
        list[0].TrafficLimitBytes=200
        err=saveDelegatedAdminsLocked(list)
    }
    delegatedAdminsMu.Unlock()
    if err!=nil{t.Fatal(err)}
    if got:=call(http.MethodPost,"/api/clients");got!=http.StatusNoContent{
        t.Fatalf("traffic renewed POST status=%d",got)
    }

    delegatedAdminsMu.Lock()
    list,err=loadDelegatedAdminsLocked()
    if err==nil{
        list[0].Active=false
        err=saveDelegatedAdminsLocked(list)
    }
    delegatedAdminsMu.Unlock()
    if err!=nil{t.Fatal(err)}
    if got:=call(http.MethodGet,"/api/me");got!=http.StatusForbidden{
        t.Fatalf("disabled delegated GET status=%d",got)
    }
}
