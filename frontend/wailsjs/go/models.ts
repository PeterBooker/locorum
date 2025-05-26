export namespace types {
	
	export class Site {
	    id: string;
	    name: string;
	    slug: string;
	    domain: string;
	    filesDir: string;
	    started: boolean;
	    phpVersion: string;
	    mysqlVersion: string;
	    redisVersion: string;
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Site(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.slug = source["slug"];
	        this.domain = source["domain"];
	        this.filesDir = source["filesDir"];
	        this.started = source["started"];
	        this.phpVersion = source["phpVersion"];
	        this.mysqlVersion = source["mysqlVersion"];
	        this.redisVersion = source["redisVersion"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}

}

