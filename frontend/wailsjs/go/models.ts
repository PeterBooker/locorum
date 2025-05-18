export namespace types {
	
	export class Site {
	    id: string;
	    name: string;
	    slug: string;
	    domain: string;
	
	    static createFrom(source: any = {}) {
	        return new Site(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.slug = source["slug"];
	        this.domain = source["domain"];
	    }
	}

}

